package mw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// TraceContext implements W3C Trace Context propagation. It parses an inbound
// traceparent header (preserving the trace-id), generates a new span-id for
// this hop, stores both in the request context, and sets traceparent on the
// response.
func TraceContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := traceExtractOrGenerate(r)
		ctx := context.WithValue(r.Context(), ctxkeys.TraceID, traceID)
		ctx = context.WithValue(ctx, ctxkeys.SpanID, spanID)
		w.Header().Set("traceparent", fmt.Sprintf("00-%s-%s-01", traceID, spanID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// traceExtractOrGenerate parses an incoming traceparent and preserves the
// trace-id while generating a fresh span-id for this hop. If no valid
// traceparent is present, both IDs are newly generated.
func traceExtractOrGenerate(r *http.Request) (traceID, spanID string) {
	tp := r.Header.Get("traceparent")
	if tp != "" {
		// Format: version(2)-traceID(32)-parentID(16)-flags(2)
		parts := strings.Split(tp, "-")
		if len(parts) == 4 && traceHex(parts[1], 32) && traceHex(parts[2], 16) {
			return parts[1], newHexID(8)
		}
	}
	return newHexID(16), newHexID(8)
}

// traceHex reports whether s is a W3C Trace Context ID: n lowercase hex digits,
// not all zero. Anything else is replaced, so no other character reaches the
// outbound traceparent, a log line or a span.
func traceHex(s string, n int) bool {
	if len(s) != n || strings.Trim(s, "0") == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// newHexID generates a random n-byte value encoded as a lowercase hex string.
func newHexID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
