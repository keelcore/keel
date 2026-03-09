package mw

import (
	"net"
	"net/http"
	"strings"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/logging"
)

// AccessLog wraps next to emit a structured access log line after each request.
// It reads RequestID, TraceID, and SpanID from the request context so it must
// be layered inside (i.e. wrapped by) RequestID and TraceContext middleware.
func AccessLog(log *logging.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aw := &accessWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(aw, r)

		fields := map[string]any{
			"method": r.Method,
			"path":   r.URL.Path,
			"status": aw.status,
			"bytes":  aw.bytesOut,
			"ip":     clientIP(r),
		}
		ctx := r.Context()
		if id, ok := ctx.Value(ctxkeys.RequestID).(string); ok && id != "" {
			fields["req_id"] = id
		}
		if tid, ok := ctx.Value(ctxkeys.TraceID).(string); ok && tid != "" {
			fields["trace_id"] = tid
		}
		if sid, ok := ctx.Value(ctxkeys.SpanID).(string); ok && sid != "" {
			fields["span_id"] = sid
		}
		log.Info("access", fields)
	})
}

// clientIP extracts the client IP, preferring the first entry of X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("x-forwarded-for"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// accessWriter captures the status code and bytes written for access logging.
type accessWriter struct {
	http.ResponseWriter
	status   int
	bytesOut int64
}

func (aw *accessWriter) WriteHeader(code int) {
	aw.status = code
	aw.ResponseWriter.WriteHeader(code)
}

func (aw *accessWriter) Write(b []byte) (int, error) {
	n, err := aw.ResponseWriter.Write(b)
	aw.bytesOut += int64(n)
	return n, err
}
