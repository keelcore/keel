package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// TraceContext generates fresh IDs when no traceparent is present and sets the
// response traceparent header while storing both IDs in the request context.
func TestTraceContext_GeneratesWhenAbsent(t *testing.T) {
	var traceID, spanID string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		traceID, _ = r.Context().Value(ctxkeys.TraceID).(string)
		spanID, _ = r.Context().Value(ctxkeys.SpanID).(string)
	})
	h := TraceContext(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if len(traceID) != 32 {
		t.Errorf("expected 32-char trace ID, got %q", traceID)
	}
	if len(spanID) != 16 {
		t.Errorf("expected 16-char span ID, got %q", spanID)
	}
	if rr.Header().Get("traceparent") == "" {
		t.Error("expected traceparent header set on response")
	}
}

// TraceContext preserves the inbound trace-id and generates a fresh span-id.
func TestTraceContext_PreservesInboundTraceID(t *testing.T) {
	var traceID, spanID string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		traceID, _ = r.Context().Value(ctxkeys.TraceID).(string)
		spanID, _ = r.Context().Value(ctxkeys.SpanID).(string)
	})
	h := TraceContext(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	h.ServeHTTP(rr, req)

	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected inbound trace ID preserved, got %q", traceID)
	}
	if len(spanID) != 16 {
		t.Errorf("expected fresh 16-char span ID, got %q", spanID)
	}
	if spanID == "00f067aa0ba902b7" {
		t.Error("expected a fresh span ID, not the inbound parent ID")
	}
}

// TraceContext generates a fresh trace-id when the inbound traceparent is malformed.
func TestTraceContext_MalformedTraceparent_Generates(t *testing.T) {
	var traceID string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		traceID, _ = r.Context().Value(ctxkeys.TraceID).(string)
	})
	h := TraceContext(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "garbage-header")
	h.ServeHTTP(rr, req)

	if len(traceID) != 32 {
		t.Errorf("expected fresh 32-char trace ID for malformed header, got %q", traceID)
	}
}

// newHexID returns a lowercase hex string of the requested byte length.
func TestNewHexID_Length(t *testing.T) {
	if got := newHexID(8); len(got) != 16 {
		t.Errorf("expected 16 hex chars for 8 bytes, got %d (%q)", len(got), got)
	}
	if got := newHexID(16); len(got) != 32 {
		t.Errorf("expected 32 hex chars for 16 bytes, got %d (%q)", len(got), got)
	}
}
