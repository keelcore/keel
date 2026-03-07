//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const jwksTTL = 5 * time.Minute

// jwksClient is used for all JWKs endpoint fetches.
var jwksClient = &http.Client{Timeout: 10 * time.Second}

// jwksCache holds fetched JWKs sets, keyed by URL, with TTL-based expiry.
type jwksCache struct {
	mu      sync.Mutex
	entries map[string]*jwksCacheEntry
}

type jwksCacheEntry struct {
	keys      []parsedJWK
	fetchedAt time.Time
}

// parsedJWK holds a decoded JWK public key with its optional key ID.
type parsedJWK struct {
	kid string
	key any // *rsa.PublicKey or *ecdsa.PublicKey
}

func newJWKSCache() *jwksCache {
	return &jwksCache{entries: make(map[string]*jwksCacheEntry)}
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

// get returns the cached entry for url, fetching fresh data if the cache is
// missing or stale. Concurrent fetches for the same URL are not deduplicated;
// the last writer wins, which is safe for a key cache.
func (c *jwksCache) get(url string) (*jwksCacheEntry, error) {
	c.mu.Lock()
	entry, ok := c.entries[url]
	c.mu.Unlock()
	if ok && time.Since(entry.fetchedAt) < jwksTTL {
		return entry, nil
	}

	keys, err := fetchJWKS(url)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKs %s: %w", url, err)
	}
	e := &jwksCacheEntry{keys: keys, fetchedAt: time.Now()}
	c.mu.Lock()
	c.entries[url] = e
	c.mu.Unlock()
	return e, nil
}

// fetchJWKS downloads a JWK Set from url and returns the parsed keys.
func fetchJWKS(url string) ([]parsedJWK, error) {
	resp, err := jwksClient.Get(url) //nolint:noctx // URL is from trusted operator config
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
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