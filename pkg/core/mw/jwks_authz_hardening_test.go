//go:build !no_authn

package mw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// countingJWKS serves status/body from fn and counts hits.
func countingJWKS(t *testing.T, fn func(n int32) (int, string)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status, body := fn(hits.Add(1))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestRequireSecureJWKSURL(t *testing.T) {
	for _, ok := range []string{"https://idp.example/jwks", "http://localhost:8080/jwks", "http://127.0.0.1:9/jwks", "http://[::1]:9/jwks"} {
		if err := requireSecureJWKSURL(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://idp.example/jwks", "http://10.0.0.5/jwks", "http://[::1"} {
		if err := requireSecureJWKSURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

// A remote plain-http JWKS URL is refused before any request is made.
func TestFetchJWKS_RefusesRemotePlainHTTP(t *testing.T) {
	if _, err := fetchJWKS("http://idp.example/jwks"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v, want https requirement", err)
	}
}

func TestFetchJWKS_Non200Rejected(t *testing.T) {
	srv, _ := countingJWKS(t, func(int32) (int, string) { return http.StatusInternalServerError, `{"keys":[]}` })
	if _, err := fetchJWKS(srv.URL); err == nil {
		t.Fatal("expected error for a non-200 JWKS response")
	}
}

// A failed fetch is remembered: repeated lookups during the backoff do not
// refetch (an issuer outage must not amplify into one fetch per request).
func TestJWKSCache_FailureCachedWithoutRefetch(t *testing.T) {
	srv, hits := countingJWKS(t, func(int32) (int, string) { return http.StatusServiceUnavailable, "" })
	c := newJWKSCache()
	for i := 0; i < 5; i++ {
		if _, err := c.get(srv.URL); err == nil {
			t.Fatal("expected cached failure")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("JWKS fetched %d times during the failure backoff, want 1", hits.Load())
	}
	// After the backoff, it tries again.
	c.entries[srv.URL].failedAt = time.Now().Add(-2 * jwksFailureBackoff)
	_, _ = c.get(srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("no refetch after backoff (hits=%d)", hits.Load())
	}
}

// When a refresh fails, the last good keys keep being served during the backoff.
func TestJWKSCache_ServesLastGoodKeysWhenRefreshFails(t *testing.T) {
	srv, hits := countingJWKS(t, func(n int32) (int, string) {
		if n == 1 {
			return http.StatusOK, `{"keys":[]}`
		}
		return http.StatusInternalServerError, ""
	})
	c := newJWKSCache()
	if _, err := c.get(srv.URL); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	c.entries[srv.URL].fetchedAt = time.Now().Add(-2 * jwksTTL) // expire
	for i := 0; i < 3; i++ {
		e, err := c.get(srv.URL)
		if err != nil || e == nil || e.keys == nil {
			t.Fatalf("lookup %d: got (%v, %v), want last good keys", i, e, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2 (one refresh attempt, then backoff)", hits.Load())
	}
}

// Concurrent lookups for one URL share a single fetch.
func TestJWKSCache_ConcurrentLookupsShareOneFetch(t *testing.T) {
	srv, hits := countingJWKS(t, func(int32) (int, string) {
		time.Sleep(150 * time.Millisecond)
		return http.StatusOK, `{"keys":[]}`
	})
	c := newJWKSCache()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.get(srv.URL)
		}()
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("JWKS fetched %d times for concurrent lookups, want 1", hits.Load())
	}
}

func authzCfg(endpoint, transport string) config.Config {
	var cfg config.Config
	cfg.ExtAuthz = config.ExtAuthzConfig{
		Enabled: true, Endpoint: endpoint, Transport: transport,
		Timeout: config.DurationOf(2 * time.Second),
	}
	return cfg
}

func serveAuthz(t *testing.T, cfg config.Config) int {
	t.Helper()
	h := ExtAuthz(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logging.New(logging.Config{Out: io.Discard}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code
}

// A redirect from the decision endpoint is not followed and is not an allow.
func TestExtAuthz_RedirectNotFollowed(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { followed.Store(true) }))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	if code := serveAuthz(t, authzCfg(redirector.URL, "http")); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a redirecting authz endpoint", code)
	}
	if followed.Load() {
		t.Fatal("authz client followed the redirect")
	}
}

// An oversized OPA decision body is not read in full and does not allow.
func TestExtAuthz_OPABodyCapped(t *testing.T) {
	opa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"pad":"`+strings.Repeat("x", 2*maxAuthzResponseBytes)+`","result":true}`)
	}))
	t.Cleanup(opa.Close)
	if code := serveAuthz(t, authzCfg(opa.URL, "opa")); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an oversized OPA body", code)
	}
}

func TestAuthzClient_UnixSocketDoesNotFollowRedirects(t *testing.T) {
	if authzClient(config.ExtAuthzConfig{Endpoint: "unix:///tmp/opa.sock"}).CheckRedirect == nil {
		t.Fatal("unix-socket authz client follows redirects")
	}
}
