//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func FuzzParseJWK(f *testing.F) {
	f.Add([]byte(`{"kty":"RSA","kid":"k1","n":"sXchDaQebHnPiGvyDOAT4saGEUetSyo9MKLOoWFsueri23bOdgWp4Dy1WlUzewbgBHod5pcM9H95GQRV3JDXboIRROSBigeC5yjU1hGzHHyXss8UDprecbAYxknTcQkhslANGRUZmdTOQ5qTRsLAt6BTYuyvVRdhS8exSZEy_c4gs_7svlJJQ4H9_NxsiIoLwAEk7-Q3UXERGYw_75IDrGA84-lA_-Ct4eTlXHBIY2EaV7t7LjJaynVJCpkv4LKjTTAumiGUIuQhrNhZLuF_RJLqHpM2kgWFLU7-VTdL1VbC2tejvcI2BlMkEpk1BzBZI0KQB0GaDWFLN-aEAw3vRw","e":"AQAB"}`))
	f.Add([]byte(`{"kty":"EC","crv":"P-256","x":"MKBCTNIcKUSDii11ySs3526iDZ8AiTo7Tu6KPAqv7D4","y":"4Etl6SRW2YiLUrN5vfvVHuhp7x8PxltmWWlbbM4IFyM"}`))
	f.Add([]byte(`{"kty":"oct"}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		k, err := parseJWK(json.RawMessage(raw))
		if err == nil && k.key == nil {
			t.Fatalf("parseJWK returned no error and no key for %q", raw)
		}
	})
}

func ecJWKSServer(t *testing.T) *httptest.Server {
	t.Helper()
	x, y := p256JWKCoords(t)
	body := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"k1"}]}`, x, y)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func TestKeysFor_GetError(t *testing.T) {
	cache := newJWKSCache()
	tok := &jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{}}
	if _, err := cache.keysFor("http://127.0.0.1:1/jwks.json", tok); err == nil {
		t.Error("expected error from unreachable endpoint")
	}
}

func TestKeysFor_KidAndAlgMatch(t *testing.T) {
	srv := ecJWKSServer(t)
	defer srv.Close()
	cache := newJWKSCache()
	tok := &jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{"kid": "k1"}}
	key, err := cache.keysFor(srv.URL, tok)
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	if key == nil {
		t.Error("expected non-nil key")
	}
}

func TestKeysFor_KidMismatch_NotFound(t *testing.T) {
	srv := ecJWKSServer(t)
	defer srv.Close()
	cache := newJWKSCache()
	tok := &jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{"kid": "does-not-exist"}}
	if _, err := cache.keysFor(srv.URL, tok); err == nil {
		t.Error("expected not-found error for mismatched kid")
	}
}

func TestKeysFor_AlgMismatch_NotFound(t *testing.T) {
	srv := ecJWKSServer(t)
	defer srv.Close()
	cache := newJWKSCache()
	// No kid → matched by alg family only; RS256 does not match an EC key.
	tok := &jwt.Token{Method: jwt.SigningMethodRS256, Header: map[string]any{}}
	if _, err := cache.keysFor(srv.URL, tok); err == nil {
		t.Error("expected not-found error for alg mismatch")
	}
}

func TestFetchJWKS_BadJSON_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "not json at all")
	}))
	defer srv.Close()
	if _, err := fetchJWKS(srv.URL); err == nil {
		t.Error("expected decode error for non-JSON body")
	}
}

func TestParseJWK_RSA_UnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"kty":"RSA","n":123,"e":"AQAB"}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected unmarshal error for numeric n")
	}
}

func TestParseJWK_RSA_BadN(t *testing.T) {
	raw := json.RawMessage(`{"kty":"RSA","n":"!!!bad","e":"AQAB"}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad n")
	}
}

func TestParseJWK_RSA_BadE(t *testing.T) {
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"!!!bad"}`, n))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad e")
	}
}

func TestParseJWK_EC_UnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"kty":"EC","crv":123}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected unmarshal error for numeric crv")
	}
}

func TestParseJWK_EC_BadX(t *testing.T) {
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":"!!!bad","y":%q}`, y))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad x")
	}
}

func TestParseJWK_EC_BadY(t *testing.T) {
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":"!!!bad"}`, x))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected base64 error for bad y")
	}
}

// parseJWK returns an error when the base envelope fails to unmarshal.
func TestParseJWK_BaseUnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"kty":123}`)
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected error when kty is not a string")
	}
}

// fetchJWKS returns an error when the endpoint is unreachable.
func TestFetchJWKS_UnreachableEndpoint_ReturnsError(t *testing.T) {
	_, err := fetchJWKS("http://127.0.0.1:1/.well-known/jwks.json")
	if err == nil {
		t.Error("expected error for unreachable JWKS endpoint")
	}
}

// fetchJWKS parses a minimal valid JWK Set with one EC key.
func TestFetchJWKS_ValidECKey(t *testing.T) {
	// Build a minimal P-256 JWK JSON from a generated key.
	x, y := p256JWKCoords(t)
	body := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"k1"}]}`, x, y)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	keys, err := fetchJWKS(srv.URL)
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}
}

// fetchJWKS skips keys with unsupported kty.
func TestFetchJWKS_SkipsUnsupportedKty(t *testing.T) {
	body := `{"keys":[{"kty":"oct","k":"somevalue","kid":"k1"}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	keys, err := fetchJWKS(srv.URL)
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys (skipped oct type), got %d", len(keys))
	}
}

// parseJWK with unsupported kty returns an error.
func TestParseJWK_UnsupportedKty(t *testing.T) {
	raw := json.RawMessage(`{"kty":"oct","kid":"k1"}`)
	_, err := parseJWK(raw)
	if err == nil {
		t.Error("expected error for unsupported kty=oct")
	}
}

// parseJWK for EC with unsupported curve returns an error.
func TestParseJWK_EC_UnsupportedCurve(t *testing.T) {
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	rawStr := fmt.Sprintf(`{"kty":"EC","crv":"P-521","x":%q,"y":%q}`, x, y)
	raw := json.RawMessage(rawStr)
	_, err := parseJWK(raw)
	if err == nil {
		t.Error("expected error for unsupported curve P-521")
	}
}

// jwksCache.get returns an error when the endpoint is unreachable.
func TestJWKSCache_Get_UnreachableEndpoint(t *testing.T) {
	cache := newJWKSCache()
	_, err := cache.get("http://127.0.0.1:1/.well-known/jwks.json")
	if err == nil {
		t.Error("expected error from cache.get for unreachable endpoint")
	}
}

// jwksCache.get returns the cached entry on the second call (TTL not expired).
func TestJWKSCache_Get_CacheHit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		x, y := p256JWKCoords(t)
		body := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"k1"}]}`, x, y)
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	cache := newJWKSCache()
	e1, err := cache.get(srv.URL)
	if err != nil {
		t.Fatalf("first cache.get: %v", err)
	}

	// Second call must hit cache (no new HTTP request).
	e2, err := cache.get(srv.URL)
	if err != nil {
		t.Fatalf("second cache.get: %v", err)
	}
	if e1 != e2 {
		t.Error("expected same cache entry on second call (cache hit)")
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d (cache not used)", calls)
	}
}

// parseJWK for RSA returns a non-nil key.
func TestParseJWK_RSA_Valid(t *testing.T) {
	// Minimal RSA JWK: n=4 bytes, e=3 bytes (exponent 65537).
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x00, 0x01})
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	rawStr := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":%q,"kid":"rsa1"}`, n, e)
	raw := json.RawMessage(rawStr)
	k, err := parseJWK(raw)
	if err != nil {
		t.Fatalf("parseJWK RSA: %v", err)
	}
	if k.key == nil {
		t.Error("expected non-nil RSA key")
	}
}

// fetchJWKS parses an RSA JWK.
func TestFetchJWKS_ValidRSAKey(t *testing.T) {
	n := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x00, 0x01})
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	body := fmt.Sprintf(`{"keys":[{"kty":"RSA","n":%q,"e":%q,"kid":"rsa1"}]}`, n, e)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	keys, err := fetchJWKS(srv.URL)
	if err != nil {
		t.Fatalf("fetchJWKS: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 RSA key, got %d", len(keys))
	}
}

// parseECJWK with P-384 curve returns a key.
func TestParseJWK_EC_P384_Valid(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Uncompressed SEC 1 point: 0x04 || X || Y, 48-byte coordinates for P-384.
	point, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(point[1:49])
	y := base64.RawURLEncoding.EncodeToString(point[49:])
	rawStr := fmt.Sprintf(`{"kty":"EC","crv":"P-384","x":%q,"y":%q}`, x, y)
	raw := json.RawMessage(rawStr)
	k, err := parseJWK(raw)
	if err != nil {
		t.Fatalf("parseJWK EC P-384: %v", err)
	}
	if k.key == nil {
		t.Error("expected non-nil EC P-384 key")
	}
}

// parseECJWK rejects coordinates that are not a point on the curve.
func TestParseJWK_EC_P384_OffCurve(t *testing.T) {
	x := base64.RawURLEncoding.EncodeToString(make([]byte, 48))
	y := base64.RawURLEncoding.EncodeToString(make([]byte, 48))
	raw := json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-384","x":%q,"y":%q}`, x, y))
	if _, err := parseJWK(raw); err == nil {
		t.Error("expected an error for an off-curve P-384 point")
	}
}

// parseECJWK rejects a coordinate longer than the curve's field size.
func TestParseJWK_EC_P256_OversizeCoordinate(t *testing.T) {
	x, y := p256JWKCoords(t)
	long := base64.RawURLEncoding.EncodeToString(make([]byte, 33))
	for _, c := range []struct{ name, x, y string }{{"x", long, y}, {"y", x, long}} {
		raw := json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, c.x, c.y))
		if _, err := parseJWK(raw); err == nil {
			t.Errorf("expected an error for a 33-byte P-256 %s coordinate", c.name)
		}
	}
}

func TestJWKAlgMatches_RSA_RS256(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	if !jwkAlgMatches(&key.PublicKey, "RS256") {
		t.Error("expected true for RSA key with RS256")
	}
}

func TestJWKAlgMatches_RSA_WrongAlg(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	if jwkAlgMatches(&key.PublicKey, "ES256") {
		t.Error("expected false for RSA key with ES256")
	}
}

func TestJWKAlgMatches_ECDSA_ES256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if !jwkAlgMatches(&key.PublicKey, "ES256") {
		t.Error("expected true for ECDSA key with ES256")
	}
}

func TestJWKAlgMatches_Unknown_ReturnsFalse(t *testing.T) {
	// A non-RSA, non-ECDSA key type returns false.
	if jwkAlgMatches("not-a-key", "RS256") {
		t.Error("expected false for unknown key type")
	}
}

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
