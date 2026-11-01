package mw

import (
	"context"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

// Limits enforces the request body cap, the response body cap, and the
// per-request read timeout. It is independent of the OWASP headers: turning the
// headers off (security.owasp_headers: false, or the no_owasp build) must not
// silently lift resource limits. OWASP applies Limits itself; when OWASP is off,
// the server applies Limits directly.
func Limits(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// Unwrap exposes the wrapped writer so http.ResponseController can reach the connection's Flusher —
// required for live streaming (SSE) passthrough through this middleware.
func (lw *limitedResponseWriter) Unwrap() http.ResponseWriter { return lw.ResponseWriter }

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
