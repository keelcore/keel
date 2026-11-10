package mw

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestTraceHex(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want bool
	}{
		{"4bf92f3577b34da6a3ce929d0e0e4736", 32, true},
		{"00f067aa0ba902b7", 16, true},
		{"4BF92F3577B34DA6A3CE929D0E0E4736", 32, false},
		{"00000000000000000000000000000000", 32, false},
		{"4bf92f3577b34da6a3ce929d0e0e47;x", 32, false},
		{"00f067aa0ba902b", 16, false},
	} {
		if got := traceHex(tc.in, tc.n); got != tc.want {
			t.Errorf("traceHex(%q, %d) = %v, want %v", tc.in, tc.n, got, tc.want)
		}
	}
}

// A traceparent whose IDs are not lowercase hex starts a new trace.
func TestTraceContext_RejectsNonHexIDs(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e47;x-00f067aa0ba902b7-01")
	traceID, _ := traceExtractOrGenerate(r)
	if traceID == "4bf92f3577b34da6a3ce929d0e0e47;x" || !traceHex(traceID, 32) {
		t.Fatalf("trace id %q, want a newly generated one", traceID)
	}
}

func TestTraceContext_GeneratesTraceparent(t *testing.T) {
	h := TraceContext(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	tp := rr.Header().Get("traceparent")
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4-part traceparent, got %q", tp)
	}
	if parts[0] != "00" {
		t.Errorf("expected version 00, got %q", parts[0])
	}
	if len(parts[1]) != 32 {
		t.Errorf("expected 32-char trace-id, got %q (len=%d)", parts[1], len(parts[1]))
	}
	if len(parts[2]) != 16 {
		t.Errorf("expected 16-char span-id, got %q (len=%d)", parts[2], len(parts[2]))
	}
}

// Per docs/observability.md §2.2: Keel generates a fresh span_id for this hop.
// The response traceparent must carry a different span_id than the inbound one.
func TestTraceContext_FreshSpanIDPerHop(t *testing.T) {
	const incomingSpanID = "00f067aa0ba902b7"
	const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-" + incomingSpanID + "-01"

	h := TraceContext(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("traceparent", incoming)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	tp := rr.Header().Get("traceparent")
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4-part traceparent response header, got %q", tp)
	}
	if parts[2] == incomingSpanID {
		t.Errorf("response span_id %q is identical to inbound span_id — expected a freshly generated span_id per hop", incomingSpanID)
	}
}

func TestTraceContext_PropagatesExistingTraceID(t *testing.T) {
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const incoming = "00-" + incomingTraceID + "-00f067aa0ba902b7-01"

	var ctxTraceID string
	h := TraceContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	h := TraceContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxSpanID, _ = r.Context().Value(ctxkeys.SpanID).(string)
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if len(ctxSpanID) != 16 {
		t.Errorf("expected 16-char span ID in context, got %q (len=%d)", ctxSpanID, len(ctxSpanID))
	}
}
