package mw

import (
	"context"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

func OWASP(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("x-content-type-options", "nosniff")
		h.Set("x-frame-options", "DENY")
		h.Set("referrer-policy", "no-referrer")
		h.Set("content-security-policy", "default-src 'none'")
		h.Set("permissions-policy", "geolocation=()")
		// HSTS is set only on TLS connections by the TLS server layer.

		if cfg.Security.MaxRequestBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.Security.MaxRequestBodyBytes)
		}

		if cfg.Timeouts.Read.Duration > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeouts.Read.Duration)
			defer cancel()
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}
