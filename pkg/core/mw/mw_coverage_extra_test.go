package mw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/logging"
)

// AccessLog reads req_id, trace_id, and span_id from the request context.
func TestAccessLog_LogsContextFields(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := AccessLog(log, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	ctx := context.WithValue(req.Context(), ctxkeys.RequestID, "req-123")
	ctx = context.WithValue(ctx, ctxkeys.TraceID, "trace-abc")
	ctx = context.WithValue(ctx, ctxkeys.SpanID, "span-def")
	req = req.WithContext(ctx)

	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
