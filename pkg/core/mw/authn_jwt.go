//go:build !no_authn

package mw

import (
	"errors"
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("authorization")
		if auth == "" || !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		raw := strings.TrimSpace(auth[len("bearer "):])

		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unsupported alg")
			}
			if len(cfg.Authn.TrustedSigners) == 0 {
				return nil, errors.New("no trusted signers configured")
			}
			return []byte(cfg.Authn.TrustedSigners[0]), nil
		})
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
