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
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/tests/unit"
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

func authnHandler(cfg config.Config) http.Handler {
	return AuthnJWT(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logging.New(logging.Config{Out: io.Discard}))
}

func bearerReq(t *testing.T, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("authorization", "Bearer "+token)
	return req
}

func signRS256(t *testing.T, priv *rsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signRS256WithKid(t *testing.T, priv *rsa.PrivateKey, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signES256WithKid(t *testing.T, priv *ecdsa.PrivateKey, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signES256(t *testing.T, priv *ecdsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signES384(t *testing.T, priv *ecdsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES384, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// jwksServerWith serves the given JSON keys array.
func jwksServerWith(t *testing.T, keys []any) *httptest.Server {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
}

func rsaJWK(pub *rsa.PublicKey, kid string) map[string]any {
	e := big.NewInt(int64(pub.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(e),
	}
}

// ecCoord returns the base64url JWK coordinate (0 = x, 1 = y) of pub: the fixed-size
// field element from its uncompressed SEC 1 encoding (0x04 || X || Y), per RFC 7518 §6.2.1.
func ecCoord(pub *ecdsa.PublicKey, i int) string {
	b, err := pub.Bytes()
	if err != nil {
		panic("ecdsa.PublicKey.Bytes: " + err.Error())
	}
	size := (len(b) - 1) / 2
	return base64.RawURLEncoding.EncodeToString(b[1+i*size : 1+(i+1)*size])
}

func ecJWK(pub *ecdsa.PublicKey, kid, crv string) map[string]any {
	return map[string]any{
		"kty": "EC",
		"kid": kid,
		"crv": crv,
		"x":   ecCoord(pub, 0),
		"y":   ecCoord(pub, 1),
	}
}

// RSA key in JWKS, kid in token — happy path.
func TestJWKS_RSA_AcceptedWithKid(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "k1")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256WithKid(t, priv, "k1")))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// RSA key in JWKS, no kid in token — alg-only matching.
func TestJWKS_RSA_AlgOnlyMatch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// Serve key without kid.
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	// Token has no kid — keysFor falls through to alg matching.
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// EC P-256 key in JWKS — happy path; covers parseECJWK P-256.
func TestJWKS_EC_P256_Accepted(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{ecJWK(&priv.PublicKey, "ec1", "P-256")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256WithKid(t, priv, "ec1")))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// EC P-384 key in JWKS; covers parseECJWK P-384.
func TestJWKS_EC_P384_Accepted(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{ecJWK(&priv.PublicKey, "ec2", "P-384")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES384(t, priv)))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// Cache hit: fetch once, shut server, second request still works.
func TestJWKS_CacheHit(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "k1")})

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	h := authnHandler(cfg)

	// First request: populates cache.
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, bearerReq(t, signRS256WithKid(t, priv, "k1")))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rr1.Code)
	}

	// Shut down the JWKS server; cache must serve the second request.
	srv.Close()

	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, bearerReq(t, signRS256WithKid(t, priv, "k1")))
	if rr2.Code != http.StatusOK {
		t.Errorf("second request (cache hit): expected 200, got %d", rr2.Code)
	}
}

// Unsupported kty in JWKS: key is skipped → no match → 401.
func TestJWKS_UnsupportedKty_Skipped(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{
		map[string]any{"kty": "oct", "k": "secret"}, // unsupported
	})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in RSA JWK n field: key skipped → 401.
func TestJWKS_RSA_BadBase64N(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "RSA",
		"kid": "bad",
		"n":   "!!!not-valid-base64url",
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in RSA JWK e field: key skipped → 401.
func TestJWKS_RSA_BadBase64E(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "RSA",
		"kid": "bad",
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   "!!!not-valid-base64url",
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Unsupported EC curve in JWKS: key skipped → 401.
func TestJWKS_EC_UnsupportedCurve(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "EC",
		"kid": "bad",
		"crv": "P-521", // unsupported
		"x":   ecCoord(&priv.PublicKey, 0),
		"y":   ecCoord(&priv.PublicKey, 1),
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	ecPriv, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES512, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString(ecPriv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, raw))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in EC JWK x field: key skipped → 401.
func TestJWKS_EC_BadBase64X(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "EC",
		"kid": "bad",
		"crv": "P-256",
		"x":   "!!!not-valid-base64url",
		"y":   ecCoord(&priv.PublicKey, 1),
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Bad base64 in EC JWK y field: key skipped → 401.
func TestJWKS_EC_BadBase64Y(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badKey := map[string]any{
		"kty": "EC",
		"kid": "bad",
		"crv": "P-256",
		"x":   ecCoord(&priv.PublicKey, 0),
		"y":   "!!!not-valid-base64url",
	}
	srv := jwksServerWith(t, []any{badKey})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// Kid mismatch: token kid doesn't match any JWK kid → no match → 401.
func TestJWKS_KidMismatch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&priv.PublicKey, "server-kid")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	// Token has kid="wrong" which won't match "server-kid".
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256WithKid(t, priv, "wrong")))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// RSA JWKS with ES256 token: jwkAlgMatches(rsa, "ES256") = false → no match → 401.
func TestJWKS_AlgMismatch_RSAKeyECToken(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{rsaJWK(&rsaPriv.PublicKey, "")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	// ES256 token: jwkAlgMatches(*rsa.PublicKey, "ES256") → false
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, ecPriv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// EC JWKS with RS256 token: jwkAlgMatches(ec, "RS256") = false → no match → 401.
func TestJWKS_AlgMismatch_ECKeyRSAToken(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksServerWith(t, []any{ecJWK(&ecPriv.PublicKey, "", "P-256")})
	defer srv.Close()

	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	// RS256 token: jwkAlgMatches(*ecdsa.PublicKey, "RS256") → false
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, rsaPriv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// JWKS endpoint returns invalid JSON: fetchJWKS decode error → 401.
func TestJWKS_FetchError_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// JWKS endpoint unreachable: HTTP fetch fails → 401.
func TestJWKS_FetchError_ServerDown(t *testing.T) {
	srv := jwksServerWith(t, []any{})
	url := srv.URL
	srv.Close() // shut down immediately; subsequent fetches fail

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{url},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseJWK json.Unmarshal error: element is a JSON number, not an object.
func TestJWKS_ParseJWK_UnmarshalError(t *testing.T) {
	// Serve {"keys": [42]} — each key element must be an object; a number causes
	// json.Unmarshal into the base struct to fail.
	payload := []byte(`{"keys": [42]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseRSAJWK json.Unmarshal error: n field is a number, not a string.
func TestJWKS_ParseRSAJWK_UnmarshalError(t *testing.T) {
	// Valid kty="RSA" for base decode, but n is a JSON number → error in parseRSAJWK.
	payload := []byte(`{"keys": [{"kty":"RSA","kid":"x","n":123,"e":"AQAB"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signRS256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// parseECJWK json.Unmarshal error: x field is a number, not a string.
func TestJWKS_ParseECJWK_UnmarshalError(t *testing.T) {
	payload := []byte(`{"keys": [{"kty":"EC","kid":"x","crv":"P-256","x":123,"y":"dGVzdA"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Authn: config.AuthnConfig{
		TrustedSigners: []string{srv.URL},
		Enabled:        true,
	}}
	rr := httptest.NewRecorder()
	authnHandler(cfg).ServeHTTP(rr, bearerReq(t, signES256(t, priv)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestJWKSClient_UsesTheTLSPolicy(t *testing.T) {
	unit.AssertOutboundPolicy(t, "jwks", jwksClient)
}
