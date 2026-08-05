//go:build !no_acme

package acme

// Seam-injection tests that exercise the otherwise-unreachable library-error
// and timing branches of manager.go without contacting a real ACME CA. Each
// test overrides a package-level seam (genKey, afterFunc, marshalECKey,
// systemCertPool, buildClient) or crafts malformed cert bytes, then restores
// the seam via t.Cleanup. Reuses helpers defined in the sibling test files
// (writeCachedCert, stubACMEClient, makeECDSACertDER) — same package.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/keelcore/keel/pkg/config"
)

// ---------------------------------------------------------------------------
// CertExpiry — parse-error branch
// ---------------------------------------------------------------------------

func TestCertExpiry_BadCertBytes(t *testing.T) {
	mgr := New()
	mgr.cert.Store(&tls.Certificate{Certificate: [][]byte{{0x00}}})
	if _, err := mgr.CertExpiry(); err == nil {
		t.Error("expected parse error for malformed cert bytes, got nil")
	}
}

// ---------------------------------------------------------------------------
// setupACMEClient / loadOrCreateAccountKey — key-generation error branch
// ---------------------------------------------------------------------------

func TestSetupACMEClient_KeyGenError(t *testing.T) {
	orig := genKey
	genKey = func(elliptic.Curve, io.Reader) (*ecdsa.PrivateKey, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { genKey = orig })

	mgr := New()
	// Empty CacheDir → loadOrCreateAccountKey skips the cache load and hits genKey.
	if _, err := mgr.setupACMEClient(config.ACMEConfig{}); err == nil {
		t.Error("expected account-key error, got nil")
	}
}

// ---------------------------------------------------------------------------
// waitForRenewalWindow — timer-fired (return true) branch
// ---------------------------------------------------------------------------

func TestWaitForRenewalWindow_TimerFires(t *testing.T) {
	orig := afterFunc
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	afterFunc = func(time.Duration) <-chan time.Time { return ch }
	t.Cleanup(func() { afterFunc = orig })

	if !waitForRenewalWindow(context.Background(), nil) {
		t.Error("expected true when renewal timer fires")
	}
}

// ---------------------------------------------------------------------------
// Validate — trivial pass
// ---------------------------------------------------------------------------

func TestValidate_ReturnsNil(t *testing.T) {
	if err := Validate(config.Config{}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// registerAccount — HTTP 409 conflict (already-registered) branch
// ---------------------------------------------------------------------------

func TestRegisterAccount_ConflictTreatedAsSuccess(t *testing.T) {
	stub := &stubACMEClient{
		registerFn: func(context.Context, *xacme.Account, func(string) bool) (*xacme.Account, error) {
			return nil, &xacme.Error{StatusCode: http.StatusConflict}
		},
	}
	// Non-empty email exercises the mailto contact branch as well.
	if err := registerAccount(context.Background(), stub, "ops@example.com"); err != nil {
		t.Errorf("409 conflict should be treated as success, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Start — register-error, fresh-cert return, and renewal-loop branches
//
// All three override the buildClient seam to inject a stub acmeClient so Start
// never contacts a CA.
// ---------------------------------------------------------------------------

func withStubClient(t *testing.T, stub acmeClient) {
	t.Helper()
	orig := buildClient
	buildClient = func(*Manager, config.ACMEConfig) (acmeClient, error) { return stub, nil }
	t.Cleanup(func() { buildClient = orig })
}

func TestStart_RegisterError(t *testing.T) {
	withStubClient(t, &stubACMEClient{
		registerFn: func(context.Context, *xacme.Account, func(string) bool) (*xacme.Account, error) {
			return nil, errors.New("boom")
		},
	})

	mgr := New()
	// Background (non-cancelled) ctx → register error is not context.Canceled.
	if err := mgr.Start(context.Background(), config.ACMEConfig{Domains: []string{"example.com"}}); err == nil {
		t.Error("expected register error to propagate, got nil")
	}
}

func TestStart_FreshCachedCertReturnsNil(t *testing.T) {
	dir := t.TempDir()
	// 60 days remaining → certNeedsRenewal is false.
	writeCachedCert(t, dir, []string{"example.com"}, time.Now().Add(60*24*time.Hour))

	withStubClient(t, &stubACMEClient{}) // default Register returns success

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // waitForRenewalWindow observes Done → returns false → Start returns nil

	mgr := New()
	cfg := config.ACMEConfig{Domains: []string{"example.com"}, CacheDir: dir}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if mgr.cert.Load() == nil {
		t.Error("fresh cached cert should be loaded")
	}
}

func TestStart_EntersRenewalLoop(t *testing.T) {
	// No cached cert → certNeedsRenewal(nil) is true → Start proceeds to
	// runRenewalLoop. AuthorizeOrder returns context.Canceled so obtainCert
	// short-circuits and the loop exits on the cancelled ctx.
	withStubClient(t, &stubACMEClient{
		authorizeOrderFn: func(context.Context, []xacme.AuthzID, ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, context.Canceled
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	if err := mgr.Start(ctx, config.ACMEConfig{Domains: []string{"example.com"}}); err != nil {
		t.Fatalf("expected nil from cancelled renewal loop, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// storeCert — writeCertPEM error after MkdirAll succeeds
// ---------------------------------------------------------------------------

func TestStoreCert_WriteCertPEMError(t *testing.T) {
	dir := t.TempDir()
	// Pre-create cert.crt as a directory so writeCertPEM's OpenFile fails
	// even though MkdirAll(cacheDir) succeeds.
	if err := os.Mkdir(filepath.Join(dir, "cert.crt"), 0o700); err != nil {
		t.Fatal(err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(60*24*time.Hour))

	mgr := New()
	if err := mgr.storeCert(dir, []string{"example.com"}, key, [][]byte{der}); err == nil {
		t.Error("expected writeCertPEM error, got nil")
	}
}

// ---------------------------------------------------------------------------
// assembleTLSCert / writeKeyPEM — MarshalECPrivateKey error branch
// ---------------------------------------------------------------------------

func TestMarshalECKeyError(t *testing.T) {
	orig := marshalECKey
	marshalECKey = func(*ecdsa.PrivateKey) ([]byte, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { marshalECKey = orig })

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(60*24*time.Hour))

	if _, err := assembleTLSCert(key, [][]byte{der}); err == nil {
		t.Error("assembleTLSCert: expected marshal error, got nil")
	}
	if err := writeKeyPEM(filepath.Join(t.TempDir(), "k.pem"), key); err == nil {
		t.Error("writeKeyPEM: expected marshal error, got nil")
	}
}

// ---------------------------------------------------------------------------
// renewalDelay / validateCert — malformed leaf-bytes branches
// ---------------------------------------------------------------------------

func TestRenewalDelay_BadCertBytesFallback(t *testing.T) {
	c := &tls.Certificate{Certificate: [][]byte{{0x00}}}
	if d := renewalDelay(c); d != 24*time.Hour {
		t.Errorf("expected 24h fallback for unparseable cert, got %v", d)
	}
}

func TestValidateCert_BadCertBytes(t *testing.T) {
	c := &tls.Certificate{Certificate: [][]byte{{0x00}}}
	if err := validateCert(c, []string{"example.com"}); err == nil {
		t.Error("expected parse-leaf error, got nil")
	}
}

// ---------------------------------------------------------------------------
// httpClientWithCA — SystemCertPool error → NewCertPool fallback
// ---------------------------------------------------------------------------

func TestHTTPClientWithCA_SystemPoolErrorFallback(t *testing.T) {
	orig := systemCertPool
	systemCertPool = func() (*x509.CertPool, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { systemCertPool = orig })

	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"ca.example.com"}, time.Now().Add(365*24*time.Hour))
	caPath := filepath.Join(dir, "ca.pem")
	f, err := os.Create(caPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.Close()

	hc, err := httpClientWithCA(caPath)
	if err != nil {
		t.Fatalf("expected fallback to NewCertPool to succeed, got %v", err)
	}
	if hc == nil {
		t.Error("expected non-nil http client")
	}
}
