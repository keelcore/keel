package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/mw"
)

func TestTraceContext_PropagatesExistingTraceID(t *testing.T) {
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const incoming = "00-" + incomingTraceID + "-00f067aa0ba902b7-01"

	var ctxTraceID string
	h := mw.TraceContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxTraceID, _ = r.Context().Value(ctxkeys.TraceID).(string)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("traceparent", incoming)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if ctxTraceID != incomingTraceID {
		t.Errorf("expected trace ID %q in context, got %q", incomingTraceID, ctxTraceID)
	}
}

func TestTraceContext_SetsSpanIDInContext(t *testing.T) {
	var ctxSpanID string
	h := mw.TraceContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxSpanID, _ = r.Context().Value(ctxkeys.SpanID).(string)
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if len(ctxSpanID) != 16 {
		t.Errorf("expected 16-char span ID in context, got %q (len=%d)", ctxSpanID, len(ctxSpanID))
	}
}
