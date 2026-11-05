package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// AuditForbidden reports each 403 next answers (from the application or the upstream).
func AuditForbidden(record func(audit.Event), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &forbiddenWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == http.StatusForbidden {
			principal, _ := r.Context().Value(ctxkeys.Principal).(string)
			record(audit.Event{Event: "forbidden", Principal: principal, SourceIP: clientIP(r), Outcome: "deny", Detail: r.URL.Path})
		}
	})
}

type forbiddenWriter struct {
	http.ResponseWriter
	status int
}

func (w *forbiddenWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped writer to http.ResponseController (streaming through the proxy).
func (w *forbiddenWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
