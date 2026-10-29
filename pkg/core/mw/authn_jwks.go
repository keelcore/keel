//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwksTTL = 5 * time.Minute
	// jwksFailureBackoff is how long a failed fetch is remembered, so an issuer
	// outage cannot turn every bearer request into an outbound fetch.
	jwksFailureBackoff = 30 * time.Second
	// maxJWKSBytes caps how much of a JWK Set response is read.
	maxJWKSBytes = 1 << 20
)

// jwksClient is used for all JWKs endpoint fetches.
var jwksClient = &http.Client{Timeout: 10 * time.Second}

// jwksCache holds fetched JWKs sets, keyed by URL, with TTL-based expiry.
// Fetches for one URL are serialized (one in flight; waiters reuse its result).
type jwksCache struct {
	mu       sync.Mutex
	entries  map[string]*jwksCacheEntry
	fetching map[string]*sync.Mutex
}

// jwksCacheEntry holds the last good keys (if any) and the last failure (if
// any). After a failure, last good keys keep being served until the backoff
// ends — a brief issuer outage does not fail every request.
type jwksCacheEntry struct {
	keys      []parsedJWK
	fetchedAt time.Time
	err       error
	failedAt  time.Time
}

// usable returns the entry's result if it may be served without a fetch now.
func (e *jwksCacheEntry) usable(now time.Time) (*jwksCacheEntry, error, bool) {
	if e.keys != nil && now.Sub(e.fetchedAt) < jwksTTL {
		return e, nil, true
	}
	if e.err != nil && now.Sub(e.failedAt) < jwksFailureBackoff {
		if e.keys != nil {
			return e, nil, true // serve last good keys during the backoff
		}
		return nil, e.err, true
	}
	return nil, nil, false
}

// parsedJWK holds a decoded JWK public key with its optional key ID.
type parsedJWK struct {
	kid string
	key any // *rsa.PublicKey or *ecdsa.PublicKey
}

func newJWKSCache() *jwksCache {
	return &jwksCache{entries: make(map[string]*jwksCacheEntry), fetching: make(map[string]*sync.Mutex)}
}

// keysFor returns the best-matching public key from the JWKs endpoint for t.
// Keys are matched first by kid (if present in the token header), then by
// algorithm family. Returns an error if no matching key is found.
func (c *jwksCache) keysFor(url string, t *jwt.Token) (any, error) {
	entry, err := c.get(url)
	if err != nil {
		return nil, err
	}
	kid, _ := t.Header["kid"].(string)
	alg := t.Method.Alg()

	for _, k := range entry.keys {
		if kid != "" && k.kid != kid {
			continue
		}
		if jwkAlgMatches(k.key, alg) {
			return k.key, nil
		}
	}
	return nil, fmt.Errorf("no JWK found for kid=%q alg=%s at %s", kid, alg, url)
}

// jwkAlgMatches reports whether key is compatible with the given JWT algorithm.
func jwkAlgMatches(key any, alg string) bool {
	switch key.(type) {
	case *rsa.PublicKey:
		return alg == "RS256" || alg == "RS384" || alg == "RS512"
	case *ecdsa.PublicKey:
		return alg == "ES256" || alg == "ES384" || alg == "ES512"
	}
	return false
}

// get returns the keys for url: from cache while fresh (or, after a failure,
// for the backoff window), otherwise from one fetch shared by concurrent callers.
func (c *jwksCache) get(url string) (*jwksCacheEntry, error) {
	if e, err, ok := c.lookup(url); ok {
		return e, err
	}
	c.mu.Lock()
	fl, ok := c.fetching[url]
	if !ok {
		fl = &sync.Mutex{}
		c.fetching[url] = fl
	}
	c.mu.Unlock()

	fl.Lock()
	defer fl.Unlock()
	// Another caller may have completed the fetch while this one waited.
	if e, err, ok := c.lookup(url); ok {
		return e, err
	}

	keys, err := fetchJWKS(url)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.entries[url]
	if err != nil {
		err = fmt.Errorf("fetch JWKs %s: %w", url, err)
		e := &jwksCacheEntry{err: err, failedAt: now}
		if prev != nil && prev.keys != nil {
			e.keys, e.fetchedAt = prev.keys, prev.fetchedAt
		}
		c.entries[url] = e
		if e.keys != nil {
			return e, nil
		}
		return nil, err
	}
	e := &jwksCacheEntry{keys: keys, fetchedAt: now}
	c.entries[url] = e
	return e, nil
}

// lookup returns a cached result for url if it may be served without a fetch.
func (c *jwksCache) lookup(url string) (*jwksCacheEntry, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[url]; ok {
		return e.usable(time.Now())
	}
	return nil, nil, false
}

// fetchJWKS downloads a JWK Set from url and returns the parsed keys. Keys must
// arrive over https — plain http lets an on-path attacker substitute keys and
// forge any token — except from loopback. Only a 200 is accepted, and at most
// maxJWKSBytes are read.
func fetchJWKS(rawURL string) ([]parsedJWK, error) {
	if err := requireSecureJWKSURL(rawURL); err != nil {
		return nil, err
	}
	resp, err := jwksClient.Get(rawURL) //nolint:noctx // URL is from trusted operator config
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKs endpoint returned %d", resp.StatusCode)
	}

	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJWKSBytes)).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode JWKs: %w", err)
	}

	out := make([]parsedJWK, 0, len(set.Keys))
	for _, raw := range set.Keys {
		k, err := parseJWK(raw)
		if err != nil {
			continue // skip unrecognized or unsupported key types
		}
		out = append(out, k)
	}
	return out, nil
}

// parseJWK decodes a single JWK object into a parsedJWK.
func parseJWK(raw json.RawMessage) (parsedJWK, error) {
	var base struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &base); err != nil {
		return parsedJWK{}, err
	}
	switch base.Kty {
	case "RSA":
		key, err := parseRSAJWK(raw)
		if err != nil {
			return parsedJWK{}, err
		}
		return parsedJWK{kid: base.Kid, key: key}, nil
	case "EC":
		key, err := parseECJWK(raw)
		if err != nil {
			return parsedJWK{}, err
		}
		return parsedJWK{kid: base.Kid, key: key}, nil
	default:
		return parsedJWK{}, fmt.Errorf("unsupported JWK kty: %s", base.Kty)
	}
}

// parseRSAJWK constructs an *rsa.PublicKey from the base64url-encoded n and e fields.
func parseRSAJWK(raw json.RawMessage) (*rsa.PublicKey, error) {
	var jwk struct {
		N string `json:"n"`
		E string `json:"e"`
	}
	if err := json.Unmarshal(raw, &jwk); err != nil {
		return nil, err
	}
	n, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("decode JWK n: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("decode JWK e: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}, nil
}

// parseECJWK constructs an *ecdsa.PublicKey from the crv, x, and y fields.
func parseECJWK(raw json.RawMessage) (*ecdsa.PublicKey, error) {
	var jwk struct {
		Crv string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	if err := json.Unmarshal(raw, &jwk); err != nil {
		return nil, err
	}
	var curve elliptic.Curve
	switch jwk.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	default:
		return nil, fmt.Errorf("unsupported JWK curve: %s", jwk.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("decode JWK x: %w", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("decode JWK y: %w", err)
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(x),
		Y:     new(big.Int).SetBytes(y),
	}, nil
}

// requireSecureJWKSURL rejects a JWKS URL that is not https, unless it targets
// loopback (a local issuer or test fixture, where there is no network path).
func requireSecureJWKSURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse JWKs URL: %w", err)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("JWKs URL %s must use https", rawURL)
}
