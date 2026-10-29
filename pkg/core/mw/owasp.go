//go:build !no_owasp

package mw

import (
	"fmt"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

// OWASP sets the OWASP security headers, then applies Limits (body caps and
// read timeout) — the limits are shared with the no-headers path, not owned here.
func OWASP(cfg config.Config, next http.Handler) http.Handler {
	limited := Limits(cfg, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("x-content-type-options", "nosniff")
		h.Set("x-frame-options", "DENY")
		h.Set("referrer-policy", "no-referrer")
		h.Set("content-security-policy", "default-src 'none'")
		h.Set("permissions-policy", "geolocation=()")
		if r.TLS != nil && cfg.Security.HSTSMaxAge > 0 {
			h.Set("strict-transport-security", fmt.Sprintf("max-age=%d", cfg.Security.HSTSMaxAge))
		}

		limited.ServeHTTP(w, r)
	})
}
