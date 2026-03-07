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