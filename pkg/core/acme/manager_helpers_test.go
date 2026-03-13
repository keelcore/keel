//go:build !no_acme

package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/keelcore/keel/pkg/config"
)

// ---------------------------------------------------------------------------
// buildAuthzIDs
// ---------------------------------------------------------------------------

// buildAuthzIDs with no domains returns an empty (non-nil) slice.
func TestBuildAuthzIDs_Empty(t *testing.T) {
	ids := buildAuthzIDs(nil)
	if ids == nil {
		t.Fatal("expected non-nil slice for nil domains")
	}
	if len(ids) != 0 {
		t.Errorf("expected len=0, got %d", len(ids))
	}
}

// buildAuthzIDs maps each domain to a dns-type AuthzID with the correct value.
func TestBuildAuthzIDs_MultiDomain(t *testing.T) {
	domains := []string{"example.com", "www.example.com"}
	ids := buildAuthzIDs(domains)
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	for i, d := range domains {
		if ids[i].Type != "dns" {
			t.Errorf("[%d] expected Type=dns, got %q", i, ids[i].Type)
		}
		if ids[i].Value != d {
			t.Errorf("[%d] expected Value=%q, got %q", i, d, ids[i].Value)
		}
	}
}

// ---------------------------------------------------------------------------
// generateCertMaterial
// ---------------------------------------------------------------------------

// generateCertMaterial returns a non-nil ECDSA P-256 key and a parseable CSR
// containing the requested domain in its DNS names.
func TestGenerateCertMaterial_Valid(t *testing.T) {
	key, csr, err := generateCertMaterial([]string{"example.com"})
	if err != nil {
		t.Fatalf("generateCertMaterial: %v", err)
	}
	if key == nil {
		t.Fatal("expected non-nil key")
	}
	if _, ok := key.Public().(*ecdsa.PublicKey); !ok {
		t.Error("expected ECDSA public key")
	}
	parsed, err := x509.ParseCertificateRequest(csr)
	if err != nil {
		t.Fatalf("parse CSR: %v", err)
	}
	if len(parsed.DNSNames) == 0 || parsed.DNSNames[0] != "example.com" {
		t.Errorf("expected DNS name example.com in CSR, got %v", parsed.DNSNames)
	}
}

// ---------------------------------------------------------------------------
// selectHTTP01Challenge
// ---------------------------------------------------------------------------

// selectHTTP01Challenge returns (nil, nil) when the authorization is already valid.
func TestSelectHTTP01Challenge_AlreadyValid(t *testing.T) {
	authz := &xacme.Authorization{Status: xacme.StatusValid}
	chal, err := selectHTTP01Challenge(authz)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if chal != nil {
		t.Error("expected nil challenge for already-valid authorization")
	}
}

// selectHTTP01Challenge returns an error when no http-01 challenge is present.
func TestSelectHTTP01Challenge_NoneAvailable(t *testing.T) {
	authz := &xacme.Authorization{
		Status:     xacme.StatusPending,
		Identifier: xacme.AuthzID{Value: "example.com"},
		Challenges: []*xacme.Challenge{{Type: "dns-01", Token: "tok"}},
	}
	chal, err := selectHTTP01Challenge(authz)
	if err == nil {
		t.Error("expected error when no http-01 challenge available")
	}
	if chal != nil {
		t.Error("expected nil challenge on error")
	}
}

// selectHTTP01Challenge returns the http-01 challenge when one is present.
func TestSelectHTTP01Challenge_Found(t *testing.T) {
	want := &xacme.Challenge{Type: "http-01", Token: "mytoken"}
	authz := &xacme.Authorization{
		Status:     xacme.StatusPending,
		Challenges: []*xacme.Challenge{{Type: "dns-01"}, want},
	}
	chal, err := selectHTTP01Challenge(authz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chal != want {
		t.Error("expected the http-01 challenge to be returned")
	}
}

// ---------------------------------------------------------------------------
// resolveDirectoryURL
// ---------------------------------------------------------------------------

// resolveDirectoryURL returns the Let's Encrypt URL when caURL is empty.
func TestResolveDirectoryURL_Empty(t *testing.T) {
	if got := resolveDirectoryURL(""); got != xacme.LetsEncryptURL {
		t.Errorf("expected LetsEncryptURL, got %q", got)
	}
}

// resolveDirectoryURL returns the custom URL unchanged.
func TestResolveDirectoryURL_Custom(t *testing.T) {
	custom := "https://acme.example.com/directory"
	if got := resolveDirectoryURL(custom); got != custom {
		t.Errorf("expected %q, got %q", custom, got)
	}
}

// ---------------------------------------------------------------------------
// waitForRenewalWindow
// ---------------------------------------------------------------------------

// waitForRenewalWindow returns false immediately when ctx is already cancelled.
func TestWaitForRenewalWindow_CancelledReturnsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForRenewalWindow(ctx, nil) {
		t.Error("expected false for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// loadAndValidateCachedCert
// ---------------------------------------------------------------------------

// loadAndValidateCachedCert returns nil without storing a cert when cacheDir is empty.
func TestLoadAndValidateCachedCert_EmptyCacheDir(t *testing.T) {
	m := New()
	cfg := config.ACMEConfig{CacheDir: "", Domains: []string{"example.com"}}
	if err := m.loadAndValidateCachedCert(cfg); err != nil {
		t.Errorf("expected nil error for empty cacheDir, got %v", err)
	}
	if m.cert.Load() != nil {
		t.Error("expected no cert stored for empty cacheDir")
	}
}

// loadAndValidateCachedCert returns nil when no cached cert files exist (cache miss).
func TestLoadAndValidateCachedCert_CacheMiss(t *testing.T) {
	m := New()
	cfg := config.ACMEConfig{CacheDir: t.TempDir(), Domains: []string{"example.com"}}
	if err := m.loadAndValidateCachedCert(cfg); err != nil {
		t.Errorf("expected nil error on cache miss, got %v", err)
	}
}

// loadAndValidateCachedCert stores a valid cached cert in the manager.
func TestLoadAndValidateCachedCert_ValidCert(t *testing.T) {
	dir := t.TempDir()
	domains := []string{"example.com"}
	writeCachedCert(t, dir, domains, time.Now().Add(90*24*time.Hour))

	m := New()
	cfg := config.ACMEConfig{CacheDir: dir, Domains: domains}
	if err := m.loadAndValidateCachedCert(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("expected cert to be stored after loading valid cache")
	}
}

// loadAndValidateCachedCert returns an error and logs when the cached cert does
// not cover the configured domains — the fatal misconfiguration branch.
func TestLoadAndValidateCachedCert_InvalidDomains(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	m := New()
	var logged string
	m.SetLogger(func(msg string, _ map[string]any) { logged = msg })
	cfg := config.ACMEConfig{CacheDir: dir, Domains: []string{"other.com"}}
	if err := m.loadAndValidateCachedCert(cfg); err == nil {
		t.Error("expected error when cached cert domains do not match config")
	}
	if logged != "acme_cached_cert_invalid" {
		t.Errorf("expected log acme_cached_cert_invalid, got %q", logged)
	}
}
