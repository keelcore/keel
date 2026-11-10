//go:build !no_sidecar

package sidecar

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/tests/unit"
)

// closeErrBody is a response body whose Close fails.
type closeErrBody struct{ io.Reader }

func (closeErrBody) Close() error { return errClose }

// An oversized response whose body fails to close reports both errors, so ErrorHandler still answers 502.
func TestCapResponse_TooLargeCloseErrorReported(t *testing.T) {
	resp := &http.Response{ContentLength: 64, Body: closeErrBody{strings.NewReader("")}}
	got := capResponse(4)(resp)
	if !errors.Is(got, errResponseTooLarge) || !errors.Is(got, errClose) {
		t.Fatalf("capResponse = %v, want errResponseTooLarge and the close error", got)
	}
}

// errRoundTripper is a fake transport returning a fixed error.
type errRoundTripper struct{ err error }

func (e errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

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

// An https upstream gets the policy whether or not sidecar.upstream_tls is enabled.
func TestBuildTransport_UsesTheTLSPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("enabled=%v: TLSClientConfig = %+v, want the outbound TLS policy", enabled, tr.TLSClientConfig)
		}
	}
}

// Through the proxy: honor_retry_after on retries a 503 after its delay; off passes it through.
func TestNewWithHooks_HonorRetryAfter(t *testing.T) {
	for _, honor := range []bool{true, false} {
		var calls atomic.Int32
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, "ok")
		}))
		cfg := config.Config{
			Timeouts: config.TimeoutsConfig{Write: config.DurationOf(5 * time.Second)},
			Sidecar: config.SidecarConfig{
				UpstreamURL: up.URL,
				Retry:       config.SidecarRetryConfig{DialAttempts: 1, HonorRetryAfter: honor},
			},
		}
		h, err := NewWithHooks(cfg, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil))
		up.Close()
		want, wantCalls := http.StatusOK, int32(2)
		if !honor {
			want, wantCalls = http.StatusServiceUnavailable, 1
		}
		if rec.Code != want || calls.Load() != wantCalls {
			t.Fatalf("honor=%v: status %d after %d upstream calls; want %d after %d", honor, rec.Code, calls.Load(), want, wantCalls)
		}
	}
}

// A closed port refuses every dial: the proxy retries, reports each retry and
// the final failure through its hooks, and answers 502.
func TestNewWithHooks_DialRetryThen502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	log, out := newBufLogger()
	var retries, failures int
	cfg := config.Config{Sidecar: config.SidecarConfig{
		UpstreamURL: "http://" + addr,
		Retry:       config.SidecarRetryConfig{DialAttempts: 3, DialBackoff: config.DurationOf(time.Millisecond)},
	}}
	h, err := NewWithHooks(cfg, Hooks{
		Log:        log,
		DialRetry:  func() { retries++ },
		DialFailed: func() { failures++ },
	})
	if err != nil {
		t.Fatalf("NewWithHooks: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://sidecar.local/", strings.NewReader("x")))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if retries != 2 || failures != 1 {
		t.Fatalf("hooks: retries %d failures %d, want 2 and 1", retries, failures)
	}
	if !strings.Contains(out.String(), "upstream_dial_retry") || !strings.Contains(out.String(), "upstream_dial_failed") {
		t.Fatalf("missing retry log lines:\n%s", out.String())
	}
}

// buildTransport with TLS disabled returns a non-nil transport (DefaultTransport clone).
func TestBuildTransport_Disabled_ReturnsClone(t *testing.T) {
	tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: false})
	if err != nil {
		t.Fatalf("buildTransport disabled: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
}

// buildTransport with TLS enabled and no CA/client cert returns a TLS transport.
func TestBuildTransport_Enabled_NoCANoCert_ReturnsTLSTransport(t *testing.T) {
	tr, err := buildTransport(config.UpstreamTLSConfig{Enabled: true})
	if err != nil {
		t.Fatalf("buildTransport enabled: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.TLSClientConfig == nil {
		t.Error("expected non-nil TLSClientConfig")
	}
}

// buildTransport with a non-existent CA file returns an error.
func TestBuildTransport_CAFileNotFound_ReturnsError(t *testing.T) {
	_, err := buildTransport(config.UpstreamTLSConfig{
		Enabled: true,
		CAFile:  "/nonexistent-ca-for-sidecar-test.pem",
	})
	if err == nil {
		t.Error("expected error for missing CA file")
	}
}

// buildTransport with a CA file containing no valid PEM returns an error.
func TestBuildTransport_CAFileInvalidPEM_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := buildTransport(config.UpstreamTLSConfig{
		Enabled: true,
		CAFile:  path,
	})
	if err == nil {
		t.Error("expected error for invalid PEM in CA file")
	}
}

// buildTransport with non-existent client cert/key returns an error.
func TestBuildTransport_ClientCertNotFound_ReturnsError(t *testing.T) {
	_, err := buildTransport(config.UpstreamTLSConfig{
		Enabled:        true,
		ClientCertFile: "/nonexistent-client-cert.pem",
		ClientKeyFile:  "/nonexistent-client-key.pem",
	})
	if err == nil {
		t.Error("expected error for missing client cert/key")
	}
}

// buildTransport with InsecureSkipVerify=true sets the TLS config field.
func TestBuildTransport_InsecureSkipVerify(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("skipping InsecureSkipVerify test under FIPS")
	}
	tr, err := buildTransport(config.UpstreamTLSConfig{
		Enabled:            true,
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true in TLS config")
	}
}

// applyHeaderPolicy with a strip list removes matching headers.
func TestApplyHeaderPolicy_Strip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Secret", "hidden")
	req.Header.Set("X-Keep", "visible")

	applyHeaderPolicy(req, config.HeaderPolicyConfig{
		Strip: []string{"X-Secret"},
	})

	if req.Header.Get("X-Secret") != "" {
		t.Error("expected X-Secret to be stripped")
	}
	if req.Header.Get("X-Keep") == "" {
		t.Error("expected X-Keep to remain")
	}
}

// applyHeaderPolicy with a forward allowlist removes headers not in the list.
func TestApplyHeaderPolicy_Forward_AllowlistFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Allowed", "keep")
	req.Header.Set("X-Blocked", "remove")

	applyHeaderPolicy(req, config.HeaderPolicyConfig{
		Forward: []string{"X-Allowed"},
	})

	if req.Header.Get("X-Allowed") == "" {
		t.Error("expected X-Allowed to remain after forward policy")
	}
	if req.Header.Get("X-Blocked") != "" {
		t.Error("expected X-Blocked to be removed by forward allowlist")
	}
}

// applyHeaderPolicy with empty policy leaves headers unchanged.
func TestApplyHeaderPolicy_Empty_NoChange(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom", "value")

	applyHeaderPolicy(req, config.HeaderPolicyConfig{})

	if req.Header.Get("X-Custom") == "" {
		t.Error("expected X-Custom unchanged with empty policy")
	}
}

// New with an invalid upstream URL returns an error.
func TestNew_InvalidUpstreamURL_ReturnsError(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "://invalid-url",
		},
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid upstream URL")
	}
}

// New with a valid upstream URL returns a non-nil handler.
func TestNew_ValidUpstreamURL_ReturnsHandler(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:9999",
		},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

// New with circuit breaker enabled returns a non-nil handler.
func TestNew_CircuitBreakerEnabled_ReturnsHandler(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:9999",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 3,
				ResetTimeout:     config.DurationOf(30 * time.Second),
			},
		},
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New with circuit breaker: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

// New with TLS buildTransport error (non-existent CA file) returns an error.
func TestNew_TLSBuildTransportError_ReturnsError(t *testing.T) {
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "https://127.0.0.1:9999",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled: true,
				CAFile:  "/nonexistent-ca-for-new-test.pem",
			},
		},
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error when upstream TLS CA file is missing")
	}
}

// New with a sign function wires it through without error.
func TestNew_WithSignFn_ReturnsHandler(t *testing.T) {
	signFn := func(_ *http.Request) error { return nil }
	cfg := config.Config{
		Sidecar: config.SidecarConfig{
			UpstreamURL: "http://127.0.0.1:9999",
		},
	}
	h, err := New(cfg, signFn)
	if err != nil {
		t.Fatalf("New with signFn: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

// limitBody: a body ending exactly at the cap, whose reader reports EOF on a
// separate call, reaches the one-byte probe and ends cleanly with io.EOF.
func TestLimitBody_ExactCapThenSeparateEOF(t *testing.T) {
	b := &limitBody{rc: io.NopCloser(strings.NewReader("abcd")), remaining: 4}
	got, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "abcd" {
		t.Fatalf("got %q", got)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Both branches get the default dial/handshake/idle timeouts, no idle or
// per-host connection cap, no environment proxy, HTTP/2 attempts and the
// response-header deadline.
func TestUpstreamTransport_PoolAndTimeouts(t *testing.T) {
	for _, tlsOn := range []bool{false, true} {
		sc := config.SidecarConfig{UpstreamURL: "http://127.0.0.1:1", UpstreamTLS: config.UpstreamTLSConfig{Enabled: tlsOn}}
		tr, err := upstreamTransport(sc, 7*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if tr.MaxIdleConnsPerHost != unlimitedIdle || tr.MaxIdleConns != 0 || tr.MaxConnsPerHost != 0 {
			t.Errorf("tls=%v: pool caps idle/host=%d idle=%d conns/host=%d", tlsOn, tr.MaxIdleConnsPerHost, tr.MaxIdleConns, tr.MaxConnsPerHost)
		}
		if tr.Proxy != nil || !tr.ForceAttemptHTTP2 || tr.ResponseHeaderTimeout != 7*time.Second {
			t.Errorf("tls=%v: proxy=%v h2=%v rht=%v", tlsOn, tr.Proxy != nil, tr.ForceAttemptHTTP2, tr.ResponseHeaderTimeout)
		}
		if tr.TLSHandshakeTimeout == 0 || tr.IdleConnTimeout == 0 {
			t.Errorf("tls=%v: missing default timeouts (handshake=%v idle=%v)", tlsOn, tr.TLSHandshakeTimeout, tr.IdleConnTimeout)
		}
	}
	if _, err := upstreamTransport(config.SidecarConfig{UpstreamTLS: config.UpstreamTLSConfig{Enabled: true, CAFile: "/nonexistent"}}, 0); err == nil {
		t.Error("bad CA file: expected error")
	}
}

func proxyGet(t *testing.T, cfg config.Config) *httptest.ResponseRecorder {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil))
	return rec
}

// upstream_h2c: requests reach the upstream as plaintext HTTP/2.
func TestNew_UpstreamH2C(t *testing.T) {
	var proto string
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { proto = r.Proto }))
	up.Config.Protocols = new(http.Protocols)
	up.Config.Protocols.SetUnencryptedHTTP2(true)
	up.Start()
	defer up.Close()

	var cfg config.Config
	cfg.Sidecar.UpstreamURL = up.URL
	cfg.Sidecar.UpstreamH2C = true
	if rec := proxyGet(t, cfg); rec.Code != http.StatusOK || proto != "HTTP/2.0" {
		t.Fatalf("status %d proto %q, want 200 HTTP/2.0", rec.Code, proto)
	}
}

// A unix:// upstream_url is dialed over the socket and addressed as localhost.
func TestNew_UnixSocketUpstream(t *testing.T) {
	dir, err := os.MkdirTemp("", "ks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "app.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var host string
	srv := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { host = r.Host })}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	var cfg config.Config
	cfg.Sidecar.UpstreamURL = "unix://" + sock
	if rec := proxyGet(t, cfg); rec.Code != http.StatusOK || host != "localhost" {
		t.Fatalf("status %d host %q, want 200 localhost", rec.Code, host)
	}
	if upstreamBase("http://app:9000") != "http://app:9000" {
		t.Error("http upstream_url altered")
	}
}

// A hung upstream frees the connection at timeouts.write: 502, not a hang.
func TestNew_HungUpstreamTimesOut(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer up.Close()
	defer close(release)

	var cfg config.Config
	cfg.Sidecar.UpstreamURL = up.URL
	cfg.Timeouts.Write = config.Duration{Duration: 200 * time.Millisecond}
	start := time.Now()
	if rec := proxyGet(t, cfg); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("hung upstream held the request %v", d)
	}
}

// isFIPSMode reports whether the test binary is running under FIPS enforcement
// (GOFIPS140 is set or GODEBUG contains fips140=only).
func isFIPSMode() bool {
	if os.Getenv("GOFIPS140") != "" {
		return true
	}
	for _, kv := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if strings.TrimSpace(kv) == "fips140=only" {
			return true
		}
	}
	return false
}

func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "keel-test-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

// generateTestCA produces a self-signed CA key pair.
func generateTestCA(t *testing.T) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return caKey, parsed, pemBytes
}

// signTestCert issues a cert signed by caKey/caCert.
// isClient=true → ExtKeyUsageClientAuth; false → ExtKeyUsageServerAuth.
func signTestCert(t *testing.T, caKey *ecdsa.PrivateKey, caCert *x509.Certificate, isClient bool) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := x509.ExtKeyUsageServerAuth
	if isClient {
		usage = x509.ExtKeyUsageClientAuth
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-entity"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

// mtlsTestServer is a TLS server that requires and verifies client certificates.
type mtlsTestServer struct {
	URL           string
	CAFile        string // path to CA PEM (client should trust the server cert via this CA)
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	close         func()
}

func newMTLSTestServer(t *testing.T) *mtlsTestServer {
	t.Helper()
	caKey, caCert, caPEM := generateTestCA(t)
	serverCertPEM, serverKeyPEM := signTestCert(t, caKey, caCert, false)
	clientCertPEM, clientKeyPEM := signTestCert(t, caKey, caCert, true)

	serverTLSCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS13,
	}
	srv.StartTLS()

	return &mtlsTestServer{
		URL:           srv.URL,
		CAFile:        writeTempFile(t, caPEM),
		ClientCertPEM: clientCertPEM,
		ClientKeyPEM:  clientKeyPEM,
		close:         srv.Close,
	}
}

func TestSidecarProxy_PlainHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}))
	defer upstream.Close()

	h, err := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}
}

func TestSidecarProxy_ResponseSizeCap_Returns502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("x", 200)))
	}))
	defer upstream.Close()

	h, err := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 100},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for oversized response", rr.Code)
	}
}

func TestSidecarProxy_XFF_Append(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "10.0.0.1")
	req.RemoteAddr = "192.168.1.1:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(gotXFF, "10.0.0.1") || !strings.Contains(gotXFF, "192.168.1.1") {
		t.Errorf("XFF append: got %q, want both original and client IP", gotXFF)
	}
}

func TestSidecarProxy_XFF_Replace(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "replace"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "10.0.0.1")
	req.RemoteAddr = "192.168.1.1:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotXFF != "192.168.1.1" {
		t.Errorf("XFF replace: got %q, want %q", gotXFF, "192.168.1.1")
	}
}

func TestSidecarProxy_XFF_Strip(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "strip"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "10.0.0.1")
	req.RemoteAddr = "192.168.1.1:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotXFF != "" {
		t.Errorf("XFF strip: got %q, want empty", gotXFF)
	}
}

func TestSidecarProxy_HeaderStrip(t *testing.T) {
	var gotSecret string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("x-secret")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:      true,
			UpstreamURL:  upstream.URL,
			XFFMode:      "append",
			HeaderPolicy: config.HeaderPolicyConfig{Strip: []string{"x-secret"}},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-secret", "top-secret")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotSecret != "" {
		t.Errorf("expected x-secret stripped, got %q", gotSecret)
	}
}

func TestSidecarProxy_UpstreamTLS_BadCert_Returns502(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			UpstreamTLS: config.UpstreamTLSConfig{Enabled: true}, // no CA → self-signed fails
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for untrusted upstream cert", rr.Code)
	}
}

func TestSidecarProxy_UpstreamTLS_InsecureSkipVerify_Returns200(t *testing.T) {
	if isFIPSMode() {
		t.Skip("InsecureSkipVerify is prohibited under FIPS mode")
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			UpstreamTLS: config.UpstreamTLSConfig{Enabled: true, InsecureSkipVerify: true},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with insecure_skip_verify", rr.Code)
	}
}

func TestSidecarProxy_MTLS_CorrectCert_Returns200(t *testing.T) {
	if isFIPSMode() {
		t.Skip("httptest TLS server uses non-FIPS-compliant certificates in mTLS mode")
	}
	s := newMTLSTestServer(t)
	defer s.close()

	certFile := writeTempFile(t, s.ClientCertPEM)
	keyFile := writeTempFile(t, s.ClientKeyPEM)

	h, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: s.URL,
			XFFMode:     "append",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled:        true,
				CAFile:         s.CAFile,
				ClientCertFile: certFile,
				ClientKeyFile:  keyFile,
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with correct mTLS client cert", rr.Code)
	}
}

func TestSidecarProxy_MTLS_WrongCert_Returns502(t *testing.T) {
	s := newMTLSTestServer(t)
	defer s.close()

	// Use a self-signed cert not signed by the server's CA.
	wrongCertPEM, wrongKeyPEM := unit.TLSTestCert(t, time.Now().Add(time.Hour))
	certFile := writeTempFile(t, wrongCertPEM)
	keyFile := writeTempFile(t, wrongKeyPEM)

	h, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: s.URL,
			XFFMode:     "append",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled:        true,
				CAFile:         s.CAFile,
				ClientCertFile: certFile,
				ClientKeyFile:  keyFile,
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for wrong mTLS client cert", rr.Code)
	}
}

func TestCircuitBreaker_DefaultThreshold(t *testing.T) {
	// FailureThreshold=0 and ResetTimeout=0 trigger the defaults inside newBreaker.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 0, // → default 5
				// ResetTimeout zero → default 30s
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	// Just verify the proxy works (circuit not open after one failure with default threshold 5).
	if rr.Code == 0 {
		t.Error("expected a response code")
	}
}

func TestCircuitBreaker_RoundTrip_TransportError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	url := upstream.URL
	upstream.Close() // close immediately; transport dial will fail

	h, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: url,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 10, // high threshold so circuit stays closed
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for transport error, got %d", rr.Code)
	}
}

func TestSidecarNew_BadURL(t *testing.T) {
	_, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "://bad-url",
		},
	})
	if err == nil {
		t.Error("expected error for invalid upstream URL")
	}
}

func TestSidecarProxy_NoResponseSizeCap(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "big response")
	}))
	defer upstream.Close()

	// MaxResponseBodyBytes=0 → ModifyResponse returns nil immediately.
	h, err := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 0},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestBuildTransport_CAFile_NoCerts(t *testing.T) {
	// Write a PEM file that has no CERTIFICATE block.
	caFile := writeTempFile(t, []byte("-----BEGIN PRIVATE KEY-----\nYWJj\n-----END PRIVATE KEY-----\n"))

	_, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "http://127.0.0.1:1",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled: true,
				CAFile:  caFile,
			},
		},
	})
	if err == nil {
		t.Error("expected error for CA file with no certificates")
	}
}

func TestBuildTransport_ClientCert_LoadError(t *testing.T) {
	caKey, caCert, caPEM := generateTestCA(t)
	certPEM, _ := signTestCert(t, caKey, caCert, true)
	// Write cert but use wrong key (the CA key, not the cert's key).
	caKeyDER, _ := os.ReadFile(writeTempFile(t, caPEM))
	certFile := writeTempFile(t, certPEM)
	keyFile := writeTempFile(t, caKeyDER) // wrong key → LoadX509KeyPair fails

	_, err := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "http://127.0.0.1:1",
			UpstreamTLS: config.UpstreamTLSConfig{
				Enabled:        true,
				ClientCertFile: certFile,
				ClientKeyFile:  keyFile,
			},
		},
	})
	if err == nil {
		t.Error("expected error for mismatched client cert/key")
	}
}

func TestSidecarProxy_HeaderForwardAllowlist(t *testing.T) {
	var gotAllowed, gotOther string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAllowed = r.Header.Get("x-allowed")
		gotOther = r.Header.Get("x-other")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			HeaderPolicy: config.HeaderPolicyConfig{
				Forward: []string{"x-allowed"}, // only x-allowed passes
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-allowed", "yes")
	req.Header.Set("x-other", "should-be-stripped")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotAllowed != "yes" {
		t.Errorf("x-allowed: got %q, want %q", gotAllowed, "yes")
	}
	if gotOther != "" {
		t.Errorf("x-other should be stripped by forward allowlist, got %q", gotOther)
	}
}

func TestSidecarProxy_XFF_Append_NoPort(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1" // no port → addrToIP returns raw value
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(gotXFF, "192.168.1.1") {
		t.Errorf("expected raw addr in XFF, got %q", gotXFF)
	}
}

func TestSidecarProxy_XFF_Append_NoIncomingXFF(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:4321"
	// No x-forwarded-for set → append mode sets only the client IP.
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotXFF != "10.0.0.1" {
		t.Errorf("expected only client IP in XFF, got %q", gotXFF)
	}
}

// X-Real-IP must always be set on the upstream request, regardless of XFF mode.
func TestSidecarProxy_XRealIP_AlwaysSet(t *testing.T) {
	var gotRealIP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRealIP = r.Header.Get("x-real-ip")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar:  config.SidecarConfig{Enabled: true, UpstreamURL: upstream.URL, XFFMode: "append"},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotRealIP == "" {
		t.Error("expected X-Real-IP to be set, got empty")
	}
	if gotRealIP != "192.168.1.1" {
		t.Errorf("X-Real-IP: got %q, want %q", gotRealIP, "192.168.1.1")
	}
}

// With xff_trusted_hops=1, X-Real-IP should reflect the trusted-hops-adjusted
// client IP, not the socket peer. Per docs/security.md §5.2:
//
//	XFF: "203.0.113.1, 10.0.0.1"  (attacker-forged, real-client)
//	socket peer: 10.1.2.3          (trusted LB — 1 hop)
//	xff_trusted_hops: 1
//	→ X-Real-IP = "10.0.0.1"      (rightmost non-trusted entry)
//
// NOTE: this test documents the required behaviour. It will be RED until
// the trusted-hops extraction is implemented in pkg/core/sidecar/xff.go.
func TestSidecarProxy_XFF_TrustedHops_ExtractsRealClientIP(t *testing.T) {
	var gotRealIP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRealIP = r.Header.Get("x-real-ip")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h, _ := New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:        true,
			UpstreamURL:    upstream.URL,
			XFFMode:        "append",
			XFFTrustedHops: 1,
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "203.0.113.1, 10.0.0.1")
	req.RemoteAddr = "10.1.2.3:1234" // trusted LB — 1 hop
	h.ServeHTTP(httptest.NewRecorder(), req)

	const wantRealIP = "10.0.0.1"
	if gotRealIP != wantRealIP {
		t.Errorf("X-Real-IP with xff_trusted_hops=1: got %q, want %q", gotRealIP, wantRealIP)
	}
}

// When keel signs, the client's Authorization is replaced by keel's; a signing
// failure is a 502 and the request never reaches the upstream unsigned.
func TestNew_Signing_ReplacesClientAuthAndFailsClosed(t *testing.T) {
	var gotAuth string
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotAuth = r.Header.Get("Authorization")
	}))
	defer upstream.Close()
	cfg := config.Config{Sidecar: config.SidecarConfig{UpstreamURL: upstream.URL}}

	signErr := error(nil)
	h, err := New(cfg, func(r *http.Request) error {
		if signErr != nil {
			return signErr
		}
		r.Header.Set("Authorization", "Bearer keel-token")
		return nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
		req.Header.Set("Authorization", "Bearer client-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call(); code != http.StatusOK || gotAuth != "Bearer keel-token" {
		t.Fatalf("signed: status %d auth %q", code, gotAuth)
	}
	signErr = errors.New("key unavailable")
	if code := call(); code != http.StatusBadGateway {
		t.Fatalf("sign failure: status %d, want 502", code)
	}
	if hits != 1 {
		t.Fatalf("upstream hit %d times, want 1 (unsigned request forwarded)", hits)
	}
}
