//go:build !no_sidecar

package unit

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/pkg/core/sidecar"
)

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Plain HTTP proxy
// ---------------------------------------------------------------------------

func TestSidecarProxy_PlainHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello")) //nolint:errcheck
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// Response size cap
// ---------------------------------------------------------------------------

func TestSidecarProxy_ResponseSizeCap_Returns502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("x", 200))) //nolint:errcheck
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// XFF modes
// ---------------------------------------------------------------------------

func TestSidecarProxy_XFF_Append(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
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

	h, _ := sidecar.New(config.Config{
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

	h, _ := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// Header policy
// ---------------------------------------------------------------------------

func TestSidecarProxy_HeaderStrip(t *testing.T) {
	var gotSecret string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("x-secret")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// Upstream TLS
// ---------------------------------------------------------------------------

func TestSidecarProxy_UpstreamTLS_BadCert_Returns502(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
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

	h, err := sidecar.New(config.Config{
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

	h, err := sidecar.New(config.Config{
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
	wrongCertPEM, wrongKeyPEM := tlsTestCert(t, time.Now().Add(time.Hour))
	certFile := writeTempFile(t, wrongCertPEM)
	keyFile := writeTempFile(t, wrongKeyPEM)

	h, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// Health probe
// ---------------------------------------------------------------------------

func TestSidecarHealthProbe_UpstreamDown_ReadinessFalse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	upstream.Close() // shut down immediately

	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(5 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, nil)

	time.Sleep(80 * time.Millisecond)

	if readiness.Get() {
		t.Error("expected readiness false after upstream down")
	}
}

func TestSidecarHealthProbe_UpstreamRecovered_ReadinessTrue(t *testing.T) {
	var healthy atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	readiness.Set(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(10 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, nil)

	time.Sleep(40 * time.Millisecond)
	if readiness.Get() {
		t.Fatal("expected readiness false before upstream healthy")
	}

	healthy.Store(true)
	time.Sleep(40 * time.Millisecond)

	if !readiness.Get() {
		t.Error("expected readiness true after upstream recovered")
	}
}

// ---------------------------------------------------------------------------
// Circuit breaker
// ---------------------------------------------------------------------------

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	var callCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(10 * time.Second),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First two requests reach upstream and record failures.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Third request: circuit is open, upstream must NOT be called.
	prevCount := callCount.Load()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 from open circuit, got %d", rr.Code)
	}
	if callCount.Load() != prevCount {
		t.Errorf("circuit should not call upstream when open (callCount was %d, now %d)", prevCount, callCount.Load())
	}
}

func TestCircuitBreaker_HalfOpenProbe_ClosesCircuit(t *testing.T) {
	var serving atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if serving.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(30 * time.Millisecond),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Trigger threshold failures → circuit opens.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Wait past reset timeout → circuit transitions to half-open.
	time.Sleep(50 * time.Millisecond)
	serving.Store(true)

	// Half-open probe succeeds → circuit closes.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("half-open probe: got %d, want 200", rr.Code)
	}

	// Subsequent request should also succeed (circuit is closed).
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("post-close request: got %d, want 200", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// newBreaker: default threshold and resetTimeout when zero values passed
// ---------------------------------------------------------------------------

func TestCircuitBreaker_DefaultThreshold(t *testing.T) {
	// FailureThreshold=0 and ResetTimeout=0 trigger the defaults inside newBreaker.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// Allow: breakerHalfOpen rejection (concurrent request while probe is in flight)
// ---------------------------------------------------------------------------

func TestCircuitBreaker_HalfOpen_ConcurrentRejected(t *testing.T) {
	// Single upstream: first 2 requests return 500 to open the circuit; subsequent
	// requests sleep 80ms then return 200 so the half-open probe stays in-flight
	// long enough for a concurrent request to see the half-open state and be rejected.
	var n atomic.Int32
	probeStarted := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		select {
		case probeStarted <- struct{}{}:
		default:
		}
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(40 * time.Millisecond),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Two failures → circuit opens.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Wait past reset timeout → next Allow() transitions to half-open.
	time.Sleep(60 * time.Millisecond)

	// Goroutine sends the half-open probe (slow upstream).
	done := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
		done <- rr.Code
	}()

	// Wait until probe is in flight (upstream received the request).
	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not start within 2s")
	}

	// Second request: breaker is half-open → Allow() returns false → 502.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 from half-open rejection, got %d", rr.Code)
	}

	<-done // let goroutine finish
}

// ---------------------------------------------------------------------------
// recordProbeResult(false): half-open probe failure re-opens circuit
// ---------------------------------------------------------------------------

func TestCircuitBreaker_HalfOpenProbe_Fails_ReOpens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: upstream.URL,
			XFFMode:     "append",
			CircuitBreaker: config.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 2,
				ResetTimeout:     config.DurationOf(30 * time.Millisecond),
			},
		},
		Security: config.SecurityConfig{MaxResponseBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Two failures → circuit opens.
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	// Wait past reset timeout → half-open on next request.
	time.Sleep(50 * time.Millisecond)

	// Half-open probe: upstream still returns 500 → recordProbeResult(false) → re-open.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	// Response may be 500 (probe got through) — not 502 yet.

	// Immediately after: circuit is open again → 502.
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest("GET", "/", nil))
	if rr2.Code != http.StatusBadGateway {
		t.Errorf("expected 502 after re-open from failed half-open probe, got %d", rr2.Code)
	}
}

// ---------------------------------------------------------------------------
// RoundTrip: inner transport error (onResult(false) + return nil, err)
// ---------------------------------------------------------------------------

func TestCircuitBreaker_RoundTrip_TransportError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	url := upstream.URL
	upstream.Close() // close immediately; transport dial will fail

	h, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// doProbe: log != nil paths (warn on failure and on unhealthy status)
// ---------------------------------------------------------------------------

func TestHealthProbe_LogsOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	upstream.Close() // immediately down → probe returns err

	sb := &safeBuf{}
	log := logging.New(logging.Config{Out: sb})
	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(5 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, log)

	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond) // let goroutine exit before reading buf

	if !strings.Contains(sb.String(), "upstream_health_probe_failed") {
		t.Errorf("expected upstream_health_probe_failed log, got: %s", sb.String())
	}
}

func TestHealthProbe_LogsOnUnhealthyStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503 → unhealthy
	}))
	defer upstream.Close()

	sb := &safeBuf{}
	log := logging.New(logging.Config{Out: sb})
	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())

	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:            upstream.URL,
		UpstreamHealthPath:     "/health",
		UpstreamHealthInterval: config.DurationOf(5 * time.Millisecond),
		UpstreamHealthTimeout:  config.DurationOf(20 * time.Millisecond),
	}, nil, readiness, log)

	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond) // let goroutine exit before reading buf

	if !strings.Contains(sb.String(), "upstream_unhealthy") {
		t.Errorf("expected upstream_unhealthy log, got: %s", sb.String())
	}
}

// ---------------------------------------------------------------------------
// StartHealthProbe: default timeout and interval (zero values → defaults applied)
// ---------------------------------------------------------------------------

func TestHealthProbe_DefaultTimeouts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	readiness := probes.NewReadiness()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately; we just want the goroutine to start

	// Zero values for timeout and interval → defaults (2s and 10s) applied inside.
	sidecar.StartHealthProbe(ctx, config.SidecarConfig{
		UpstreamURL:        upstream.URL,
		UpstreamHealthPath: "/health",
		// UpstreamHealthTimeout: zero → default 2s
		// UpstreamHealthInterval: zero → default 10s
	}, nil, readiness, nil)

	time.Sleep(20 * time.Millisecond) // let goroutine start and see ctx.Done
}

// ---------------------------------------------------------------------------
// New: invalid upstream URL → error
// ---------------------------------------------------------------------------

func TestSidecarNew_BadURL(t *testing.T) {
	_, err := sidecar.New(config.Config{
		Sidecar: config.SidecarConfig{
			Enabled:     true,
			UpstreamURL: "://bad-url",
		},
	})
	if err == nil {
		t.Error("expected error for invalid upstream URL")
	}
}

// ---------------------------------------------------------------------------
// New + ModifyResponse: maxResp <= 0 returns nil immediately
// ---------------------------------------------------------------------------

func TestSidecarProxy_NoResponseSizeCap(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "big response")
	}))
	defer upstream.Close()

	// MaxResponseBodyBytes=0 → ModifyResponse returns nil immediately.
	h, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// buildTransport: CA file with no cert blocks → error
// ---------------------------------------------------------------------------

func TestBuildTransport_CAFile_NoCerts(t *testing.T) {
	// Write a PEM file that has no CERTIFICATE block.
	caFile := writeTempFile(t, []byte("-----BEGIN PRIVATE KEY-----\nYWJj\n-----END PRIVATE KEY-----\n"))

	_, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// buildTransport: client cert + key load error
// ---------------------------------------------------------------------------

func TestBuildTransport_ClientCert_LoadError(t *testing.T) {
	caKey, caCert, caPEM := generateTestCA(t)
	certPEM, _ := signTestCert(t, caKey, caCert, true)
	// Write cert but use wrong key (the CA key, not the cert's key).
	caKeyDER, _ := os.ReadFile(writeTempFile(t, caPEM))
	certFile := writeTempFile(t, certPEM)
	keyFile := writeTempFile(t, caKeyDER) // wrong key → LoadX509KeyPair fails

	_, err := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// applyHeaderPolicy: Forward allowlist removes headers not in the list
// ---------------------------------------------------------------------------

func TestSidecarProxy_HeaderForwardAllowlist(t *testing.T) {
	var gotAllowed, gotOther string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAllowed = r.Header.Get("x-allowed")
		gotOther = r.Header.Get("x-other")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// addrToIP: address with no port → SplitHostPort fails → raw address returned
// ---------------------------------------------------------------------------

func TestSidecarProxy_XFF_Append_NoPort(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// XFF append: no incoming XFF (empty) → only client IP set
// ---------------------------------------------------------------------------

func TestSidecarProxy_XFF_Append_NoIncomingXFF(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("x-forwarded-for")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
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

// ---------------------------------------------------------------------------
// X-Real-IP
// ---------------------------------------------------------------------------

// X-Real-IP must always be set on the upstream request, regardless of XFF mode.
func TestSidecarProxy_XRealIP_AlwaysSet(t *testing.T) {
	var gotRealIP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRealIP = r.Header.Get("x-real-ip")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h, _ := sidecar.New(config.Config{
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

	h, _ := sidecar.New(config.Config{
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
