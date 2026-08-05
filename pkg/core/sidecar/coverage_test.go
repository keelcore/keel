//go:build !no_sidecar

package sidecar

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// errRoundTripper is a fake transport returning a fixed error.
type errRoundTripper struct{ err error }

func (e errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

// okRoundTripper is a fake transport returning a fixed status code.
type okRoundTripper struct{ status int }

func (o okRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: o.status,
		Body:       io.NopCloser(nil),
		Request:    req,
	}, nil
}

// ---------------------------------------------------------------------------
// circuit.go — unreachable default via invalid state
// ---------------------------------------------------------------------------

// Allow returns false for an unrecognized breaker state (defensive default).
func TestBreaker_Allow_InvalidState_ReturnsFalse(t *testing.T) {
	b := newBreaker(3, time.Second)
	b.state = breakerState(99)
	allowed, onResult := b.Allow()
	if allowed {
		t.Error("expected Allow=false for invalid state")
	}
	if onResult != nil {
		t.Error("expected nil onResult for invalid state")
	}
}

// ---------------------------------------------------------------------------
// cbTransport.RoundTrip — success and inner-error paths
// ---------------------------------------------------------------------------

// RoundTrip returns the upstream error and records failure when inner fails.
func TestCBTransport_InnerError_RecordsFailure(t *testing.T) {
	b := newBreaker(5, time.Minute)
	boom := errors.New("boom")
	ct := &cbTransport{inner: errRoundTripper{err: boom}, breaker: b}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	_, err := ct.RoundTrip(req)
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
	if b.failures != 1 {
		t.Errorf("expected failures=1 after inner error, got %d", b.failures)
	}
}

// RoundTrip returns the response and records success when inner succeeds (<500).
func TestCBTransport_Success_RecordsSuccess(t *testing.T) {
	b := newBreaker(5, time.Minute)
	b.failures = 2
	ct := &cbTransport{inner: okRoundTripper{status: http.StatusOK}, breaker: b}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	resp, err := ct.RoundTrip(req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 response, got %v", resp)
	}
	if b.failures != 0 {
		t.Errorf("expected failures reset to 0 after success, got %d", b.failures)
	}
}

// ---------------------------------------------------------------------------
// buildTransport — CA pool and client cert success paths, and the
// DefaultTransport-not-*http.Transport fallback.
// ---------------------------------------------------------------------------

// genSelfSigned writes a self-signed ECDSA cert (+key) to dir and returns paths.
func genSelfSigned(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sidecar-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// buildTransport with a valid CA file populates RootCAs.
func TestBuildTransport_CAFileValid_SetsRootCAs(t *testing.T) {
	dir := t.TempDir()
	certPath, _ := genSelfSigned(t, dir)
	tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: true, CAFile: certPath})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Error("expected RootCAs populated from valid CA file")
	}
}

// buildTransport with a valid client cert/key loads the certificate (mTLS).
func TestBuildTransport_ClientCertValid_LoadsCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genSelfSigned(t, dir)
	tr, err := buildTransport(config.UpstreamTLSConfig{
		Enabled:        true,
		ClientCertFile: certPath,
		ClientKeyFile:  keyPath,
	})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	if tr.TLSClientConfig == nil || len(tr.TLSClientConfig.Certificates) != 1 {
		t.Error("expected one client certificate loaded")
	}
}

// buildTransport falls back to a bare *http.Transport when the default
// transport is not an *http.Transport.
func TestBuildTransport_DefaultNotHTTPTransport_Fallback(t *testing.T) {
	orig := httpDefaultTransport
	httpDefaultTransport = errRoundTripper{err: errors.New("x")}
	t.Cleanup(func() { httpDefaultTransport = orig })

	tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: false})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	if tr == nil {
		t.Error("expected non-nil bare transport fallback")
	}
}

// ---------------------------------------------------------------------------
// New — driving the ReverseProxy closures end-to-end
// ---------------------------------------------------------------------------

// A request driven through the proxy exercises Rewrite (XFF, header policy,
// sign fn) and ModifyResponse's normal (capped) path.
func TestNew_ProxySuccess_DrivesRewriteAndModifyResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	defer upstream.Close()

	signed := false
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL:    upstream.URL,
			XFFMode:        "append",
			XFFTrustedHops: 0,
			HeaderPolicy:   config.HeaderPolicyConfig{Strip: []string{"X-Drop"}},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1024},
	}
	h, err := New(cfg, func(*http.Request) error { signed = true; return nil })
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://sidecar.local/path", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	req.Header.Set("X-Drop", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rec.Body.String())
	}
	if !signed {
		t.Error("expected sign function to be invoked")
	}
}

// ModifyResponse returns nil early when max_response_body_bytes is unset (<=0).
func TestNew_ProxySuccess_NoBodyCap(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("uncapped"))
	}))
	defer upstream.Close()

	cfg := config.Config{Sidecar: config.SidecarConfig{UpstreamURL: upstream.URL}}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "uncapped" {
		t.Errorf("expected 'uncapped', got %q", rec.Body.String())
	}
}

// An oversized upstream body trips errResponseTooLarge → 502 via ErrorHandler.
func TestNew_ResponseTooLarge_ErrorHandler502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("way too much data for the cap"))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Sidecar:  config.SidecarConfig{UpstreamURL: upstream.URL},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 4},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for oversized response, got %d", rec.Code)
	}
}

// A read error from the response body surfaces as a 502 (default ErrorHandler).
func TestNew_ModifyResponseReadError_ErrorHandler502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()

	orig := ioReadAll
	ioReadAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read boom") }
	t.Cleanup(func() { ioReadAll = orig })

	cfg := config.Config{
		Sidecar:  config.SidecarConfig{UpstreamURL: upstream.URL},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1024},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on body read error, got %d", rec.Code)
	}
}

// A dead upstream produces a transport error → default ErrorHandler 502; with a
// circuit breaker (threshold=1) the following request fast-fails as circuit-open.
func TestNew_TransportError_ThenCircuitOpen(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:1",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 1,
				ResetTimeout:     config.DurationOf(time.Minute),
			},
		},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First request: connection refused → transport error → default 502.
	req1 := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on transport error, got %d", rec1.Code)
	}

	// Second request: breaker is now open → circuit-open 502 branch.
	req2 := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on open circuit, got %d", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// health.go — default timeout/interval, ticker tick, and log-set warn paths
// ---------------------------------------------------------------------------

// StartHealthProbe applies default timeout and interval when both are zero.
func TestStartHealthProbe_DefaultsApplied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	cfg := config.SidecarConfig{
		UpstreamURL:        upstream.URL,
		UpstreamHealthPath: "/health",
		// timeout and interval left at zero → defaults applied.
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartHealthProbe(ctx, cfg, nil, readiness, nil)
	time.Sleep(30 * time.Millisecond)
}

// StartHealthProbe fires the ticker at least once with a short interval.
func TestStartHealthProbe_TickerFires(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	cfg := config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(2 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(time.Second),
	}
	ctx, cancel := context.WithCancel(context.Background())
	StartHealthProbe(ctx, cfg, nil, readiness, nil)
	time.Sleep(40 * time.Millisecond)
	cancel()
	if hits.Load() < 2 {
		t.Errorf("expected ticker to fire multiple probes, got %d", hits.Load())
	}
}

// doProbe logs on connection error when a logger is set.
func TestDoProbe_ConnectionError_WithLog(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	readiness := probes.NewReadiness()
	readiness.Set(true)
	client := &http.Client{Timeout: 50 * time.Millisecond}
	doProbe(client, "http://127.0.0.1:1/health", readiness, log)
	if readiness.Get() {
		t.Error("expected readiness=false on connection error")
	}
}

// doProbe logs an unhealthy status when a logger is set.
func TestDoProbe_Unhealthy_WithLog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	log := logging.New(logging.Config{Out: io.Discard})
	readiness := probes.NewReadiness()
	readiness.Set(true)
	client := &http.Client{Timeout: time.Second}
	doProbe(client, upstream.URL+"/health", readiness, log)
	if readiness.Get() {
		t.Error("expected readiness=false for unhealthy upstream")
	}
}
