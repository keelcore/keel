package mw

import (
    "context"
    "net/http"

    "github.com/keelcore/keel/pkg/config"
)

func OWASP(cfg config.Config, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("x-content-type-options", "nosniff")
        w.Header().Set("x-frame-options", "DENY")
        w.Header().Set("referrer-policy", "no-referrer")
        w.Header().Set("content-security-policy", "default-src 'none'")
        w.Header().Set("permissions-policy", "geolocation=()")

        if cfg.MaxRequestBodyBytes > 0 {
            r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxRequestBodyBytes)
        }

        if cfg.ReadTimeout > 0 {
            ctx, cancel := context.WithTimeout(r.Context(), cfg.ReadTimeout)
            defer cancel()
            r = r.WithContext(ctx)
        }

        next.ServeHTTP(w, r)
    })
}
