package mw

import (
	"context"
	"fmt"
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
		if r.TLS != nil && cfg.Security.HSTSMaxAge > 0 {
			h.Set("strict-transport-security", fmt.Sprintf("max-age=%d", cfg.Security.HSTSMaxAge))
		}

		if cfg.Security.MaxRequestBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.Security.MaxRequestBodyBytes)
		}

		if cfg.Security.MaxResponseBodyBytes > 0 {
			w = &limitedResponseWriter{ResponseWriter: w, remaining: cfg.Security.MaxResponseBodyBytes}
		}

		if cfg.Timeouts.Read.Duration > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeouts.Read.Duration)
			defer cancel()
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// limitedResponseWriter truncates response body writes at MaxResponseBodyBytes
// to prevent oversized responses from consuming excessive memory or bandwidth.
type limitedResponseWriter struct {
	http.ResponseWriter
	remaining int64
}

func (lw *limitedResponseWriter) Write(b []byte) (int, error) {
	if lw.remaining <= 0 {
		return 0, nil
	}
	if int64(len(b)) > lw.remaining {
		b = b[:lw.remaining]
	}
	n, err := lw.ResponseWriter.Write(b)
	lw.remaining -= int64(n)
	return n, err
}
