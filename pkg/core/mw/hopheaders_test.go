package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// seen records the headers a handler behind HopHeaders received.
func seen(trust bool, h http.Header) (int, http.Header) {
	var got http.Header
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Clone() })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header = h
	rec := httptest.NewRecorder()
	HopHeaders(trust, next).ServeHTTP(rec, req)
	return rec.Code, got
}

func idHeaders() http.Header {
	h := http.Header{}
	h.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	h.Set("tracestate", "vendor=x")
	h.Set("x-request-id", "client-chosen")
	return h
}

// Class C: two Authorization lines would let keel and the upstream read different credentials.
func TestHopHeaders_RepeatedAuthorizationIsRefused(t *testing.T) {
	h := http.Header{}
	h.Add("Authorization", "Bearer a")
	h.Add("Authorization", "Bearer b")
	for _, trust := range []bool{true, false} {
		code, got := seen(trust, h.Clone())
		if code != http.StatusBadRequest || got != nil {
			t.Fatalf("trust=%v: status %d, reached handler %v; want 400 and not reached", trust, code, got != nil)
		}
	}
}

// Class A: with no trusted hop in front of keel, client copies of the IDs are dropped.
func TestHopHeaders_UntrustedIDsAreDropped(t *testing.T) {
	code, got := seen(false, idHeaders())
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, name := range relayedIDHeaders {
		if v := got.Values(name); len(v) != 0 {
			t.Errorf("%s = %q, want dropped", name, v)
		}
	}
}

func TestHopHeaders_TrustedIDsAreKept(t *testing.T) {
	want := idHeaders()
	_, got := seen(true, idHeaders())
	for _, name := range relayedIDHeaders {
		if got.Get(name) != want.Get(name) {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want.Get(name))
		}
	}
}

// Class C: a repeated ID header is dropped even from a trusted hop.
func TestHopHeaders_RepeatedIDsAreDropped(t *testing.T) {
	h := idHeaders()
	for _, name := range relayedIDHeaders {
		h.Add(name, "second")
	}
	_, got := seen(true, h)
	for _, name := range relayedIDHeaders {
		if v := got.Values(name); len(v) != 0 {
			t.Errorf("%s = %q, want dropped", name, v)
		}
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
