package mw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

// newULID returns a 26-character string every time.
func TestNewULID_Length26(t *testing.T) {
	id := newULID()
	if len(id) != 26 {
		t.Errorf("expected ULID length 26, got %d (%q)", len(id), id)
	}
}

// newULID returns different values on consecutive calls.
func TestNewULID_Uniqueness(t *testing.T) {
	a := newULID()
	b := newULID()
	if a == b {
		t.Error("expected different ULIDs on consecutive calls")
	}
}

func TestRequestID_GeneratesA26CharULID(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	id := rr.Header().Get("x-request-id")
	if len(id) != 26 {
		t.Errorf("expected 26-char ULID, got %q (len=%d)", id, len(id))
	}
}

func TestRequestID_PreservesExistingID(t *testing.T) {
	const existingID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-request-id", existingID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("x-request-id"); got != existingID {
		t.Errorf("expected ID %q to be preserved, got %q", existingID, got)
	}
}

func TestRequestID_SetsContextValue(t *testing.T) {
	var ctxID string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID, _ = r.Context().Value(ctxkeys.RequestID).(string)
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	responseID := rr.Header().Get("x-request-id")
	if ctxID != responseID {
		t.Errorf("context ID %q != response header ID %q", ctxID, responseID)
	}
}

// RequestID generates a ULID when x-request-id is absent.
func TestRequestID_GeneratesULID(t *testing.T) {
	var gotID string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("x-request-id")
	})
	h := RequestID(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if gotID != "" {
		t.Errorf("inner should not get x-request-id from inbound; got %q", gotID)
	}
	if rr.Header().Get("x-request-id") == "" {
		t.Error("expected x-request-id on response when absent from request")
	}
}

// RequestID propagates an existing x-request-id header.
func TestRequestID_PropagatesExisting(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-request-id", "my-id-123")
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("x-request-id"); got != "my-id-123" {
		t.Errorf("expected x-request-id=my-id-123 echoed, got %q", got)
	}
}

// A valid client X-Request-ID is kept; an invalid or oversized one is replaced.
func TestRequestID_ValidatesInboundID(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cases := map[string]bool{
		"0b6c9a2e-7f1d-4c3a-9b8e-2d4f6a8c0e1f": true,
		"01JB4ZQ2W5Y8N6V3K7T9R1M0PX":           true,
		"evil\"id":                             false,
		"a b":                                  false,
		strings.Repeat("a", maxRequestIDLen+1): false,
	}
	for in, keep := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("x-request-id", in)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get("x-request-id")
		if keep && got != in {
			t.Errorf("valid %q replaced with %q", in, got)
		}
		if !keep && (got == in || len(got) != 26) {
			t.Errorf("invalid %q echoed as %q, want fresh ULID", in, got)
		}
	}
}
