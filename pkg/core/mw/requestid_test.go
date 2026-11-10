package mw

import (
	"net/http"
	"net/http/httptest"
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
