//go:build !no_authn

package mw

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

func AuthnJWT(cfg config.Config, next http.Handler, log *logging.Logger) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.Authn.TrustedIDs))
	for _, id := range cfg.Authn.TrustedIDs {
		allowed[id] = struct{}{}
	}
	cache := newJWKSCache()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("authorization")
		if auth == "" || !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		raw := strings.TrimSpace(auth[len("bearer "):])

		claims, err := parseWithAllSigners(raw, cfg.Authn.TrustedSigners, cache)
		if err != nil {
			log.Warn("authn_fail", map[string]any{"err": err.Error()})
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		sub, _ := claims["sub"].(string)
		if len(allowed) > 0 {
			if _, ok := allowed[sub]; !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// parseWithAllSigners tries each trusted signer until one successfully verifies raw.
func parseWithAllSigners(raw string, signers []string, cache *jwksCache) (jwt.MapClaims, error) {
	if len(signers) == 0 {
		return nil, errors.New("no trusted signers configured")
	}
	var lastErr error
	for _, s := range signers {
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, signerKeyfunc(s, cache))
		if err == nil {
			return claims, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// signerKeyfunc returns a jwt.Keyfunc for a single trusted signer entry.
func signerKeyfunc(signer string, cache *jwksCache) jwt.Keyfunc {
	return func(t *jwt.Token) (any, error) {
		if strings.HasPrefix(signer, "http://") || strings.HasPrefix(signer, "https://") {
			return cache.keysFor(signer, t)
		}
		return resolveStaticKey(signer, t.Method.Alg())
	}
}

// resolveStaticKey maps a static signer string to the appropriate verification key.
// PEM-encoded public keys are parsed as RSA or EC; all other strings are treated
// as HMAC secrets and are only accepted for HS-family algorithms.
func resolveStaticKey(signer, alg string) (any, error) {
	if strings.HasPrefix(strings.TrimSpace(signer), "-----BEGIN") {
		return parsePEMPublicKey(signer, alg)
	}
	switch alg {
	case "HS256", "HS384", "HS512":
		return []byte(signer), nil
	default:
		return nil, fmt.Errorf("signer is a secret but token uses %s", alg)
	}
}

// parsePEMPublicKey decodes a PEM public key and validates it against the
// algorithm family present in the JWT header (RS* for RSA, ES* for EC).
func parsePEMPublicKey(pemData, alg string) (any, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("no PEM block found in signer")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	switch k := pub.(type) {
	case *rsa.PublicKey:
		switch alg {
		case "RS256", "RS384", "RS512":
			return k, nil
		default:
			return nil, fmt.Errorf("RSA key cannot verify %s token", alg)
		}
	case *ecdsa.PublicKey:
		switch alg {
		case "ES256", "ES384", "ES512":
			return k, nil
		default:
			return nil, fmt.Errorf("EC key cannot verify %s token", alg)
		}
	default:
		return nil, fmt.Errorf("unsupported public key type %T", pub)
	}
}
