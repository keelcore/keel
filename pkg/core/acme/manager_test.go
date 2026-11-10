//go:build !no_acme

package acme

import (
	"context"
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	xacme "golang.org/x/crypto/acme"
)

// Tests for the ACME http-01 challenge handler.
//
// Spec reference: RFC 8555 §8.3 HTTP Challenge
// https://www.rfc-editor.org/rfc/rfc8555#section-8.3
//
// Key requirements under test:
//   - §8.3: server MUST respond 200 with the key authorization as body
//   - §8.3: Content-Type MUST be "application/octet-stream"
//   - §8.3: unknown / empty tokens MUST return 404
//   - All non-challenge paths MUST redirect to HTTPS (301)

func handlerWithToken(httpsPort int) http.Handler {
	m := New()
	m.SetToken("validtoken", "validtoken.thumbprint")
	return m.HTTPHandler(httpsPort, []string{"example.com", "www.example.com"})
}

// RFC 8555 §8.3: known token must return 200 with the key authorization as body.
func TestHTTPHandler_KnownToken_200(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/validtoken", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "validtoken.thumbprint" {
		t.Fatalf("want key-auth body %q, got %q", "validtoken.thumbprint", got)
	}
}

// RFC 8555 §8.3: Content-Type MUST be "application/octet-stream".
func TestHTTPHandler_KnownToken_ContentType(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/validtoken", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("want Content-Type application/octet-stream, got %q", ct)
	}
}

// RFC 8555 §8.3: unknown token must return 404.
func TestHTTPHandler_UnknownToken_404(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/nosuchthing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// RFC 8555 §8.3: empty token segment (bare prefix path) must return 404.
func TestHTTPHandler_EmptyToken_404(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// DeleteToken: after removal the token must no longer be served.
func TestHTTPHandler_DeleteToken_404(t *testing.T) {
	m := New()
	m.SetToken("temptoken", "temptoken.thumb")
	m.DeleteToken("temptoken")
	h := m.HTTPHandler(443, []string{"example.com"})
	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/temptoken", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 after deletion, got %d", w.Code)
	}
}

// Non-challenge path must redirect to HTTPS (301); standard port 443 → no port in host.
func TestHTTPHandler_Redirect_StandardPort(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/some/path?q=1", nil)
	r.Host = "example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("want 301, got %d", w.Code)
	}
	want := "https://example.com/some/path?q=1"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("want Location %q, got %q", want, got)
	}
}

// Non-challenge path must redirect to HTTPS; non-standard port must appear in host.
func TestHTTPHandler_Redirect_NonStandardPort(t *testing.T) {
	h := handlerWithToken(8443)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("want 301, got %d", w.Code)
	}
	want := "https://example.com:8443/"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("want Location %q, got %q", want, got)
	}
}

// Non-challenge path must redirect even when request host already carries a port.
func TestHTTPHandler_Redirect_HostWithPort(t *testing.T) {
	h := handlerWithToken(443)
	r := httptest.NewRequest(http.MethodGet, "/foo", nil)
	r.Host = "example.com:80"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("want 301, got %d", w.Code)
	}
	want := "https://example.com/foo"
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("want Location %q, got %q", want, got)
	}
}

// S10: the redirect target never comes from a client Host that is not a
// configured domain — a forged Host goes to the primary domain instead.
func TestHTTPHandler_Redirect_ForgedHostGoesToPrimaryDomain(t *testing.T) {
	h := handlerWithToken(443)
	for host, want := range map[string]string{
		"evil.example.net":   "https://example.com/x",
		"203.0.113.7:80":     "https://example.com/x",
		"WWW.Example.COM.":   "https://www.example.com/x",
		"www.example.com:80": "https://www.example.com/x",
	} {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Location"); w.Code != http.StatusMovedPermanently || got != want {
			t.Errorf("Host %q: %d %q, want 301 %q", host, w.Code, got, want)
		}
	}
}

// With no configured domains there is nowhere safe to redirect: 404.
func TestHTTPHandler_Redirect_NoDomains404(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Host = "evil.example.net"
	w := httptest.NewRecorder()
	New().HTTPHandler(443, nil).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestACME_ChallengeRouteServedWithoutRedirect(t *testing.T) {
	mgr := New()
	mgr.SetToken("abc123", "abc123.keyauth")
	h := mgr.HTTPHandler(443, []string{"example.com"})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/abc123", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for challenge, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "abc123.keyauth" {
		t.Errorf("expected key auth body, got %q", body)
	}
}

func TestACME_UnknownToken_Returns404(t *testing.T) {
	mgr := New()
	h := mgr.HTTPHandler(443, []string{"example.com"})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d", rr.Code)
	}
}

func TestACME_NonChallengePath_RedirectsToHTTPS(t *testing.T) {
	mgr := New()
	h := mgr.HTTPHandler(8443, []string{"example.com"})

	req := httptest.NewRequest("GET", "/app/path?q=1", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", rr.Code)
	}
	loc := rr.Header().Get("location")
	if !strings.HasPrefix(loc, "https://") {
		t.Errorf("expected https:// redirect, got %q", loc)
	}
	if !strings.Contains(loc, "8443") {
		t.Errorf("expected port 8443 in redirect, got %q", loc)
	}
	if !strings.Contains(loc, "/app/path?q=1") {
		t.Errorf("expected original path+query in redirect, got %q", loc)
	}
}

func TestACME_DeleteToken_RemovesChallenge(t *testing.T) {
	mgr := New()
	mgr.SetToken("tok1", "tok1.auth")
	mgr.DeleteToken("tok1")
	h := mgr.HTTPHandler(443, []string{"example.com"})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/acme-challenge/tok1", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rr.Code)
	}
}

// HTTPHandler: redirect with httpsPort=443 → host must not include ":443".
func TestACME_Redirect_Port443_NoPortInLocation(t *testing.T) {
	mgr := New()
	h := mgr.HTTPHandler(443, []string{"example.com"})

	req := httptest.NewRequest("GET", "/other/path", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rr.Code)
	}
	loc := rr.Header().Get("location")
	if !strings.HasPrefix(loc, "https://example.com/") {
		t.Errorf("expected https://example.com/... redirect, got %q", loc)
	}
	if strings.Contains(loc, ":443") {
		t.Errorf("port 443 must not appear in redirect URL, got %q", loc)
	}
}

// HTTPHandler: host includes a port (SplitHostPort succeeds → h stripped).
func TestACME_Redirect_HostWithPort_StripsSrcPort(t *testing.T) {
	mgr := New()
	h := mgr.HTTPHandler(8443, []string{"example.com"})

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com:80" // source port present → SplitHostPort succeeds
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rr.Code)
	}
	loc := rr.Header().Get("location")
	if !strings.Contains(loc, ":8443") {
		t.Errorf("expected :8443 in redirect, got %q", loc)
	}
	if strings.Contains(loc, ":80") {
		t.Errorf("source port 80 must not appear in redirect, got %q", loc)
	}
}

// Start: returns nil when context is cancelled before any CA contact.
func TestACME_Start_ExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so Start exits without dialling
	mgr := New()
	if err := mgr.Start(ctx, config.ACMEConfig{}); err != nil {
		t.Errorf("Start: expected nil on cancelled context, got %v", err)
	}
}

// Validate: always returns nil.
func TestACME_Validate_ReturnsNil(t *testing.T) {
	if err := Validate(config.Config{}); err != nil {
		t.Errorf("Validate: expected nil, got %v", err)
	}
}

func TestLoadCachedCert_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadCachedCert(dir); err == nil {
		t.Error("expected error for missing cert files, got nil")
	}
}

func TestLoadCachedCert_ValidFiles(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	cert, err := loadCachedCert(dir)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cert == nil {
		t.Error("expected non-nil cert")
	}
}

func TestCertNeedsRenewal_Nil(t *testing.T) {
	if !certNeedsRenewal(nil) {
		t.Error("nil cert should need renewal")
	}
}

func TestCertNeedsRenewal_Expired(t *testing.T) {
	c := makeTLSCert(t, []string{"example.com"}, "ecdsa", time.Now().Add(-time.Hour))
	if !certNeedsRenewal(c) {
		t.Error("expired cert should need renewal")
	}
}

func TestCertNeedsRenewal_WithinRenewalWindow(t *testing.T) {
	// 15 days left — inside the 30-day window
	c := makeTLSCert(t, []string{"example.com"}, "ecdsa", time.Now().Add(15*24*time.Hour))
	if !certNeedsRenewal(c) {
		t.Error("cert within 30-day window should need renewal")
	}
}

func TestCertNeedsRenewal_FreshCert(t *testing.T) {
	// 60 days left — outside the 30-day window
	c := makeTLSCert(t, []string{"example.com"}, "ecdsa", time.Now().Add(60*24*time.Hour))
	if certNeedsRenewal(c) {
		t.Error("cert with 60 days remaining should not need renewal")
	}
}

func TestValidateCert_Valid(t *testing.T) {
	c := makeTLSCert(t, []string{"api.example.com", "www.example.com"}, "ecdsa",
		time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"api.example.com", "www.example.com"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateCert_MissingDomain(t *testing.T) {
	c := makeTLSCert(t, []string{"api.example.com"}, "ecdsa", time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"api.example.com", "www.example.com"}); err == nil {
		t.Error("expected error for missing domain, got nil")
	}
}

func TestValidateCert_RSAKeyRejected(t *testing.T) {
	c := makeTLSCert(t, []string{"example.com"}, "rsa", time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"example.com"}); err == nil {
		t.Error("expected error for RSA key, got nil")
	}
}

func TestValidateCert_SubsetDomainsOK(t *testing.T) {
	// Cert covers three domains; config only requires two — still valid.
	c := makeTLSCert(t, []string{"a.example.com", "b.example.com", "c.example.com"}, "ecdsa",
		time.Now().Add(90*24*time.Hour))
	if err := validateCert(c, []string{"a.example.com", "b.example.com"}); err != nil {
		t.Errorf("expected nil for subset match, got %v", err)
	}
}

// TestStart_LoadsCachedCert verifies that Start() stores a pre-existing cached
// cert into m.cert before contacting the CA. The context is pre-cancelled so
// Start() exits immediately after the cache-load attempt.
func TestStart_LoadsCachedCert(t *testing.T) {
	dir := t.TempDir()
	// Write a fresh cert (60 days remaining — not in renewal window).
	writeCachedCert(t, dir, []string{"example.com"}, time.Now().Add(60*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:  []string{"example.com"},
		CacheDir: dir,
	}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: expected nil, got %v", err)
	}
	if mgr.cert.Load() == nil {
		t.Error("cert should be loaded from cache before CA contact")
	}
}

// TestStart_CachedCertWrongDomain verifies that Start() returns a non-nil
// error and does not store the cert when the cached cert's SANs don't match
// the configured domains.
func TestStart_CachedCertWrongDomain(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, []string{"old.example.com"}, time.Now().Add(60*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:  []string{"new.example.com"},
		CacheDir: dir,
	}
	if err := mgr.Start(ctx, cfg); err == nil {
		t.Error("Start: expected error for domain mismatch, got nil")
	}
	if mgr.cert.Load() != nil {
		t.Error("cert must not be stored when validation fails")
	}
}

// TestStart_CachedCertRSAKeyRejected verifies that Start() returns an error
// when the cached cert uses an RSA key, which violates Keel TLS policy.
func TestStart_CachedCertRSAKeyRejected(t *testing.T) {
	dir := t.TempDir()
	writeRSACachedCert(t, dir, []string{"example.com"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:  []string{"example.com"},
		CacheDir: dir,
	}
	if err := mgr.Start(ctx, cfg); err == nil {
		t.Error("Start: expected error for RSA cert, got nil")
	}
}

// TestStart_WithCACertFile_ValidFile verifies that Start() uses the
// CACertFile to build an http.Client when set. The context is pre-cancelled
// so Start returns after the cache load attempt.
func TestStart_WithCACertFile_ValidFile(t *testing.T) {
	cacheDir := t.TempDir()
	caDir := t.TempDir()
	// Write a fresh ECDSA cert to cache so validation passes.
	writeCachedCert(t, cacheDir, []string{"example.com"}, time.Now().Add(60*24*time.Hour))
	// Write a valid self-signed PEM cert as the CA cert.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(365*24*time.Hour))
	caPath := filepath.Join(caDir, "ca.pem")
	f, err := os.Create(caPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:    []string{"example.com"},
		CacheDir:   cacheDir,
		CACertFile: caPath,
	}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start with CACertFile: expected nil, got %v", err)
	}
}

// TestStart_WithCACertFile_InvalidFile verifies that Start() returns an error
// when CACertFile points to a non-existent file.
func TestStart_WithCACertFile_InvalidFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains:    []string{"example.com"},
		CACertFile: "/nonexistent-ca-for-test.pem",
	}
	if err := mgr.Start(ctx, cfg); err == nil {
		t.Error("Start: expected error for missing CACertFile, got nil")
	}
}

// TestStart_DefaultCAUrl verifies Start() uses LetsEncryptURL when CAUrl is empty.
// The context is pre-cancelled (after registerAccount fails with context.Canceled)
// so the function returns nil.
func TestStart_DefaultCAUrl_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so registerAccount gets context.Canceled immediately

	mgr := New()
	cfg := config.ACMEConfig{
		Domains: []string{"example.com"},
		// CAUrl empty → uses LetsEncryptURL
	}
	// registerAccount will fail with context.Canceled → Start returns nil.
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start with canceled ctx: expected nil, got %v", err)
	}
}

// TestStart_WithEmail verifies Start() passes the email to registerAccount.
// The context is pre-cancelled so the function exits after registration fails.
func TestStart_WithEmail_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := New()
	cfg := config.ACMEConfig{
		Domains: []string{"example.com"},
		Email:   "test@example.com",
	}
	if err := mgr.Start(ctx, cfg); err != nil {
		t.Fatalf("Start with email + canceled ctx: expected nil, got %v", err)
	}
}

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

// generateCertMaterial returns a non-nil ECDSA P-256 key and a parseable CSR
// containing the requested domain in its DNS names.
func TestGenerateCertMaterial_Valid(t *testing.T) {
	key, csr, err := generateCertMaterial([]string{"example.com"}, noInjection)
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

// waitForRenewalWindow returns false immediately when ctx is already cancelled.
func TestWaitForRenewalWindow_CancelledReturnsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForRenewalWindow(ctx, nil) {
		t.Error("expected false for cancelled context")
	}
}

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

func TestCertNeedsRenewal_BadCertBytes(t *testing.T) {
	// A tls.Certificate with garbage DER in Certificate[0] — ParseCertificate
	// will fail, so certNeedsRenewal must return true.
	c := &tls.Certificate{Certificate: [][]byte{[]byte("not-a-real-cert")}}
	if !certNeedsRenewal(c) {
		t.Error("cert with invalid DER should need renewal")
	}
}

func TestAssembleTLSCert_EmptyChain_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// An empty certPEM fed to tls.X509KeyPair should fail.
	if _, err := assembleTLSCert(key, [][]byte{}); err == nil {
		t.Error("expected error for empty DER chain")
	}
}

func TestStoreCert_AssembleFailure(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Empty DER chain causes assembleTLSCert to fail (X509KeyPair needs cert).
	if err := m.storeCert("", []string{"example.com"}, key, [][]byte{}); err == nil {
		t.Error("expected error for empty DER chain in storeCert")
	}
}

func TestStoreCert_MkdirFail(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	// Create a regular file then use a subpath beneath it as the cache dir.
	// MkdirAll cannot create a directory inside a file on any OS, so it always fails.
	f, err := os.CreateTemp("", "keel-acme-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())
	badPath := filepath.Join(f.Name(), "subdir")
	if err := m.storeCert(badPath, []string{"example.com"}, key, [][]byte{der}); err == nil {
		t.Error("expected error for non-writable cacheDir in storeCert")
	}
}

func TestStoreCert_ValidationFailure(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Cert covers "example.com" but we claim it should cover "other.com".
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	if err := m.storeCert("", []string{"other.com"}, key, [][]byte{der}); err == nil {
		t.Error("expected error when cert SANs do not match configured domains")
	}
}

func TestWriteCertPEM_NonWritablePath_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(time.Hour))
	// Write to a non-existent directory — should fail.
	err = writeCertPEM("/nonexistent-dir-for-test/cert.crt", [][]byte{der})
	if err == nil {
		t.Error("expected error writing to non-existent directory")
	}
}

func TestWriteKeyPEM_NonWritablePath_ReturnsError(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	err = writeKeyPEM("/nonexistent-dir-for-test/cert.key", key)
	if err == nil {
		t.Error("expected error writing to non-existent directory")
	}
}

func TestSetLogger_StoredAndCalled(t *testing.T) {
	m := New()
	called := false
	m.SetLogger(func(event string, _ map[string]any) {
		called = true
		_ = event
	})
	if m.logErr == nil {
		t.Error("expected logErr to be set after SetLogger")
	}
	m.logErr("test_event", nil)
	if !called {
		t.Error("expected logger callback to be invoked")
	}
}

func TestCertExpiry_NoCert(t *testing.T) {
	m := New()
	_, err := m.CertExpiry()
	if err == nil {
		t.Error("expected error when no certificate stored")
	}
}

func TestCertExpiry_WithCert(t *testing.T) {
	m := New()
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	m.cert.Store(c)
	secs, err := m.CertExpiry()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if secs <= 0 {
		t.Errorf("expected positive expiry seconds, got %f", secs)
	}
}

func TestGetCertificate_NoCert(t *testing.T) {
	m := New()
	_, err := m.GetCertificate(nil)
	if err == nil {
		t.Error("expected error when no certificate stored")
	}
}

func TestGetCertificate_WithCert(t *testing.T) {
	m := New()
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	m.cert.Store(c)
	got, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got == nil {
		t.Error("expected non-nil certificate")
	}
}

func TestBuildCSR_Valid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	domains := []string{"example.com", "www.example.com"}
	der, err := buildCSR(key, domains)
	if err != nil {
		t.Fatalf("buildCSR: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatalf("parse CSR: %v", err)
	}
	if csr.Subject.CommonName != domains[0] {
		t.Errorf("expected CN %q, got %q", domains[0], csr.Subject.CommonName)
	}
	if len(csr.DNSNames) != len(domains) {
		t.Errorf("expected %d DNS names, got %d", len(domains), len(csr.DNSNames))
	}
}

func TestAssembleTLSCert_Valid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(time.Hour))
	cert, err := assembleTLSCert(key, [][]byte{der})
	if err != nil {
		t.Fatalf("assembleTLSCert: %v", err)
	}
	if cert == nil {
		t.Error("expected non-nil tls.Certificate")
	}
}

func TestWriteCertPEM_CreatesFile(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(time.Hour))

	dir := t.TempDir()
	path := filepath.Join(dir, "cert.crt")
	if err := writeCertPEM(path, [][]byte{der}); err != nil {
		t.Fatalf("writeCertPEM: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cert file: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Error("no PEM block in written cert file")
	}
}

func TestRenewalDelay_NilCert(t *testing.T) {
	d := renewalDelay(nil)
	if d != 24*time.Hour {
		t.Errorf("expected 24h fallback for nil cert, got %v", d)
	}
}

func TestRenewalDelay_FreshCert(t *testing.T) {
	// 90 days remaining — delay should be ~60 days (90 - 30).
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(90*24*time.Hour))
	d := renewalDelay(c)
	// Should be positive and greater than 1 hour.
	if d < time.Hour {
		t.Errorf("expected delay > 1h for fresh cert, got %v", d)
	}
}

func TestRenewalDelay_ExpiringSoon(t *testing.T) {
	// 10 days remaining — inside the 30-day window → minimum 1h.
	c := makeMgrTLSCert(t, []string{"example.com"}, time.Now().Add(10*24*time.Hour))
	d := renewalDelay(c)
	if d != time.Hour {
		t.Errorf("expected 1h minimum for expiring cert, got %v", d)
	}
}

func TestHTTPClientWithCA_MissingFile(t *testing.T) {
	_, err := httpClientWithCA("/nonexistent-ca-for-test.pem")
	if err == nil {
		t.Error("expected error for missing CA cert file")
	}
}

func TestHTTPClientWithCA_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := httpClientWithCA(path)
	if err == nil {
		t.Error("expected error for invalid PEM content")
	}
}

func TestHTTPClientWithCA_ValidCA(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"ca.example.com"}, time.Now().Add(365*24*time.Hour))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.Close()

	client, err := httpClientWithCA(path)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if client == nil {
		t.Error("expected non-nil http.Client")
	}
}

func TestStoreCert_NoCacheDir(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	if err := m.storeCert("", []string{"example.com"}, key, [][]byte{der}); err != nil {
		t.Fatalf("storeCert with empty cacheDir: %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("cert must be stored in memory even with empty cacheDir")
	}
}

func TestStoreCert_WithCacheDir(t *testing.T) {
	m := New()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(90*24*time.Hour))

	if err := m.storeCert(dir, []string{"example.com"}, key, [][]byte{der}); err != nil {
		t.Fatalf("storeCert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cert.crt")); err != nil {
		t.Errorf("cert.crt not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cert.key")); err != nil {
		t.Errorf("cert.key not written: %v", err)
	}
}

// loadOrCreateAccountKey with empty cacheDir generates a new key.
func TestLoadOrCreateAccountKey_EmptyCacheDir(t *testing.T) {
	key, err := loadOrCreateAccountKey("")
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey with empty cacheDir: %v", err)
	}
	if key == nil {
		t.Error("expected non-nil key")
	}
}

// loadOrCreateAccountKey creates and caches a key in a temp dir.
func TestLoadOrCreateAccountKey_CreatesAndCaches(t *testing.T) {
	dir := t.TempDir()
	key1, err := loadOrCreateAccountKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey (create): %v", err)
	}
	if key1 == nil {
		t.Fatal("expected non-nil key after create")
	}

	// Second call should load from cache.
	key2, err := loadOrCreateAccountKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey (load): %v", err)
	}
	if key2 == nil {
		t.Fatal("expected non-nil key after load")
	}
	// Both keys should have the same public key.
	if !key1.PublicKey.Equal(&key2.PublicKey) {
		t.Error("expected same key on second call (loaded from cache)")
	}
}

// loadKeyPEM with a missing file returns an error.
func TestLoadKeyPEM_MissingFile(t *testing.T) {
	_, err := loadKeyPEM("/nonexistent-key-for-test.pem")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// loadKeyPEM with non-PEM content returns an error.
func TestLoadKeyPEM_NotPEM(t *testing.T) {
	dir := t.TempDir()
	path := makeFile(t, dir, "notpem.pem", []byte("not a pem"))
	_, err := loadKeyPEM(path)
	if err == nil {
		t.Error("expected error for non-PEM content")
	}
}

// loadKeyPEM with a valid EC key PEM returns the key.
func TestLoadKeyPEM_ValidKey(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := makeFile(t, dir, "key.pem", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	loaded, err := loadKeyPEM(path)
	if err != nil {
		t.Fatalf("loadKeyPEM: %v", err)
	}
	if loaded == nil {
		t.Error("expected non-nil key")
	}
}

func TestFulfillHTTP01_GetAuthorizationError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("get-auth-fail")
		},
	}
	err := m.fulfillHTTP01(context.Background(), stub, "http://ca/authz/1")
	if err == nil {
		t.Fatal("expected error from GetAuthorization failure")
	}
}

func TestFulfillHTTP01_AlreadyValid(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return &xacme.Authorization{Status: xacme.StatusValid}, nil
		},
	}
	if err := m.fulfillHTTP01(context.Background(), stub, "http://ca/authz/1"); err != nil {
		t.Fatalf("expected nil for already-valid authz, got %v", err)
	}
}

func TestFulfillHTTP01_SuccessAcceptsChallenge(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return &xacme.Authorization{
				Status: xacme.StatusPending,
				URI:    "http://ca/authz/1",
				Challenges: []*xacme.Challenge{
					{Type: "http-01", Token: "tok1"},
				},
			}, nil
		},
	}
	if err := m.fulfillHTTP01(context.Background(), stub, "http://ca/authz/1"); err != nil {
		t.Fatalf("expected nil for successful challenge, got %v", err)
	}
}

func TestAcceptChallenge_HTTP01ChallengeResponseError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		http01ChallengeResponseFn: func(_ string) (string, error) {
			return "", errors.New("key-auth-fail")
		},
	}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	err := m.acceptChallenge(context.Background(), stub, authz, chal)
	if err == nil {
		t.Fatal("expected error from HTTP01ChallengeResponse failure")
	}
}

func TestAcceptChallenge_AcceptError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		acceptFn: func(_ context.Context, _ *xacme.Challenge) (*xacme.Challenge, error) {
			return nil, errors.New("accept-fail")
		},
	}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	err := m.acceptChallenge(context.Background(), stub, authz, chal)
	if err == nil {
		t.Fatal("expected error from Accept failure")
	}
}

func TestAcceptChallenge_WaitAuthorizationError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		waitAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("wait-auth-fail")
		},
	}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	err := m.acceptChallenge(context.Background(), stub, authz, chal)
	if err == nil {
		t.Fatal("expected error from WaitAuthorization failure")
	}
}

func TestAcceptChallenge_Success(t *testing.T) {
	m := New()
	stub := &stubACMEClient{}
	authz := &xacme.Authorization{URI: "http://ca/authz/1"}
	chal := &xacme.Challenge{Type: "http-01", Token: "tok"}
	if err := m.acceptChallenge(context.Background(), stub, authz, chal); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestObtainCert_AuthorizeOrderError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, errors.New("authorize-fail")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.obtainCert(context.Background(), stub, cfg); err == nil {
		t.Fatal("expected error from AuthorizeOrder failure")
	}
}

func TestObtainCert_FulfillAllHTTP01Error(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			// Return an order with an AuthzURL so fulfillAllHTTP01 is exercised.
			return &xacme.Order{URI: "http://ca/order/1", AuthzURLs: []string{"http://ca/authz/1"}}, nil
		},
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("get-auth-fail")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.obtainCert(context.Background(), stub, cfg); err == nil {
		t.Fatal("expected error from fulfillAllHTTP01 failure")
	}
}

func TestObtainCert_WaitOrderError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return &xacme.Order{URI: "http://ca/order/1"}, nil
		},
		waitOrderFn: func(_ context.Context, _ string) (*xacme.Order, error) {
			return nil, errors.New("wait-order-fail")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.obtainCert(context.Background(), stub, cfg); err == nil {
		t.Fatal("expected error from WaitOrder failure")
	}
}

func TestObtainCert_Success(t *testing.T) {
	domains := []string{"example.com"}
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return &xacme.Order{URI: "http://ca/order/1", FinalizeURL: "http://ca/order/1/finalize"}, nil
		},
		waitOrderFn: func(_ context.Context, _ string) (*xacme.Order, error) {
			return &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}, nil
		},
		createOrderCertFn: stubSignedCert(t),
	}
	cfg := config.ACMEConfig{Domains: domains}
	if err := m.obtainCert(context.Background(), stub, cfg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("expected cert to be stored after obtainCert success")
	}
}

func TestFinalizeCert_InjectKeyError(t *testing.T) {
	m := New()
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), &stubACMEClient{}, order, []string{"example.com"}, "", injectKeyErr); err == nil {
		t.Fatal("expected error from injected key generation failure")
	}
}

func TestFinalizeCert_InjectCSRError(t *testing.T) {
	m := New()
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), &stubACMEClient{}, order, []string{"example.com"}, "", injectCSRErr); err == nil {
		t.Fatal("expected error from injected CSR build failure")
	}
}

func TestFinalizeCert_CreateOrderCertError(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		createOrderCertFn: func(_ context.Context, _ string, _ []byte, _ bool) ([][]byte, string, error) {
			return nil, "", errors.New("create-cert-fail")
		},
	}
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), stub, order, []string{"example.com"}, "", noInjection); err == nil {
		t.Fatal("expected error from CreateOrderCert failure")
	}
}

func TestFinalizeCert_Success(t *testing.T) {
	domains := []string{"example.com"}
	m := New()
	stub := &stubACMEClient{createOrderCertFn: stubSignedCert(t)}
	order := &xacme.Order{FinalizeURL: "http://ca/order/1/finalize"}
	if err := m.finalizeCert(context.Background(), stub, order, domains, "", noInjection); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if m.cert.Load() == nil {
		t.Error("expected cert to be stored after finalizeCert success")
	}
}

func TestFulfillAllHTTP01_FirstURLFails(t *testing.T) {
	m := New()
	stub := &stubACMEClient{
		getAuthorizationFn: func(_ context.Context, _ string) (*xacme.Authorization, error) {
			return nil, errors.New("get-auth-fail")
		},
	}
	err := m.fulfillAllHTTP01(context.Background(), stub, []string{"http://ca/authz/1", "http://ca/authz/2"})
	if err == nil {
		t.Fatal("expected error when first URL fails")
	}
}

func TestFulfillAllHTTP01_AllSucceed(t *testing.T) {
	m := New()
	stub := &stubACMEClient{} // default: GetAuthorization returns StatusValid
	urls := []string{"http://ca/authz/1", "http://ca/authz/2"}
	if err := m.fulfillAllHTTP01(context.Background(), stub, urls); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestRunRenewalLoop_ContextCancel verifies the loop exits when ctx is done.
// AuthorizeOrder returns context.Canceled (swallowed), then waitForRenewalWindow
// sees the done ctx and returns false.
func TestRunRenewalLoop_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := New()
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, context.Canceled
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.runRenewalLoop(ctx, stub, cfg); err != nil {
		t.Fatalf("expected nil from cancelled context, got %v", err)
	}
}

// TestRunRenewalLoop_ObtainErrorLogged verifies that a non-context.Canceled
// error from obtainCert is passed to the logger and the loop still exits
// cleanly when the context is cancelled.
func TestRunRenewalLoop_ObtainErrorLogged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := New()
	var logged string
	m.SetLogger(func(msg string, _ map[string]any) { logged = msg })
	stub := &stubACMEClient{
		authorizeOrderFn: func(_ context.Context, _ []xacme.AuthzID, _ ...xacme.OrderOption) (*xacme.Order, error) {
			return nil, errors.New("transient-error")
		},
	}
	cfg := config.ACMEConfig{Domains: []string{"example.com"}}
	if err := m.runRenewalLoop(ctx, stub, cfg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if logged != "acme_cert_obtain_failed" {
		t.Errorf("expected acme_cert_obtain_failed logged, got %q", logged)
	}
}

func TestCertExpiry_BadCertBytes(t *testing.T) {
	mgr := New()
	mgr.cert.Store(&tls.Certificate{Certificate: [][]byte{{0x00}}})
	if _, err := mgr.CertExpiry(); err == nil {
		t.Error("expected parse error for malformed cert bytes, got nil")
	}
}

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

func TestValidate_ReturnsNil(t *testing.T) {
	if err := Validate(config.Config{}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

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

// writeFileAtomic: an fsync failure leaves the old file intact and no temp
// file behind; a successful write replaces a looser-mode file with 0600.
func TestWriteFileAtomic_SyncFailureKeepsOldFile_SuccessIs0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.key")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := syncFile
	syncFile = func(*os.File) error { return errors.New("disk full") }
	err := writeFileAtomic(path, []byte("new"))
	syncFile = orig
	if err == nil {
		t.Fatal("expected sync error")
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Fatalf("old file clobbered: %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}

	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("got %q, want new", b)
	}
	// Windows has no Unix permission bits; 0600 is meaningful only elsewhere.
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
}

// storeCert writes the key first; a key write failure stops before the cert.
func TestStoreCert_WriteKeyPEMError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cert.key"), 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der := makeECDSACertDER(t, key, []string{"example.com"}, time.Now().Add(60*24*time.Hour))
	if err := New().storeCert(dir, []string{"example.com"}, key, [][]byte{der}); err == nil {
		t.Fatal("expected writeKeyPEM error, got nil")
	}
	if _, err := os.Stat(filepath.Join(dir, "cert.crt")); !os.IsNotExist(err) {
		t.Error("cert written after key write failed")
	}
}

func FuzzMatchDomain(f *testing.F) {
	f.Add("example.com", "example.com")
	f.Add("EXAMPLE.com.", "example.com")
	f.Add("evil.example.com", "example.com")
	f.Add("", "")
	f.Add(".", "example.com.")
	f.Fuzz(func(t *testing.T, host, domain string) {
		got, ok := matchDomain(host, []string{domain})
		if !ok {
			return
		}
		if got != domain {
			t.Fatalf("matchDomain(%q) returned %q, want configured domain %q", host, got, domain)
		}
		if !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(domain, ".")) {
			t.Fatalf("matchDomain matched host %q to unrelated domain %q", host, domain)
		}
	})
}

// The CA client gets the policy, with or without a custom CA, and keeps the custom roots.
func TestACMEClient_UsesTheOutboundTLSPolicy(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := buildACMEClient(key, config.ACMEConfig{})
	if err != nil {
		t.Fatal(err)
	}
	assertOutboundPolicy(t, "default CA", plain.(*xacme.Client).HTTPClient)

	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	custom, err := buildACMEClient(key, config.ACMEConfig{CACertFile: caFile})
	if err != nil {
		t.Fatal(err)
	}
	if assertOutboundPolicy(t, "custom CA", custom.(*xacme.Client).HTTPClient).RootCAs == nil {
		t.Fatal("custom CA: RootCAs dropped")
	}
}
