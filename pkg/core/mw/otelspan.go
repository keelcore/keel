//go:build !no_otel

package mw

import (
	"net/http"
	"strings"
	"time"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/tracing"
)

// OTelSpan captures per-request timing and submits a span to the OTLP
// exporter. It must be positioned inside TraceContext in the middleware stack
// so that r.Context() already carries the trace/span IDs when this handler
// runs. When getExp returns nil (tracing disabled), the request is passed
// through unchanged.
func OTelSpan(getExp func() *tracing.Exporter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// The incoming traceparent header holds the *caller's* span ID, which
		// becomes this span's parent. Extract it before the header is consumed.
		parentSpanID := incomingSpanID(r.Header.Get("traceparent"))

		ow := &otelWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ow, r)

		exp := getExp()
		if exp == nil {
			return
		}
		// TraceContext (outer) has already set these in r.Context().
		traceID, _ := r.Context().Value(ctxkeys.TraceID).(string)
		spanID, _ := r.Context().Value(ctxkeys.SpanID).(string)

		exp.Submit(tracing.Span{
			TraceID:      traceID,
			SpanID:       spanID,
			ParentSpanID: parentSpanID,
			Name:         r.Method + " " + r.URL.Path,
			Start:        start,
			End:          time.Now(),
			HTTPMethod:   r.Method,
			HTTPPath:     r.URL.Path,
			HTTPStatus:   ow.status,
		})
	})
}

// incomingSpanID extracts the parent-id field from a W3C traceparent header.
// Returns an empty string if the header is absent or malformed.
func incomingSpanID(tp string) string {
	// Format: version(2)-traceID(32)-parentID(16)-flags(2)
	parts := strings.Split(tp, "-")
	if len(parts) == 4 && len(parts[2]) == 16 {
		return parts[2]
	}
	return ""
}

// otelWriter captures the HTTP status code for span attribution.
type otelWriter struct {
	http.ResponseWriter
	status int
}

func (ow *otelWriter) WriteHeader(code int) {
	ow.status = code
	ow.ResponseWriter.WriteHeader(code)
}
