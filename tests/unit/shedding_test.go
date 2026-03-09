package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/mw"
	"github.com/keelcore/keel/pkg/core/probes"
)

func TestShedding_NotReadyReturns503(t *testing.T) {
	r := probes.NewReadiness()
	r.Set(false)
	h := mw.Shedding(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when not ready, got %d", rr.Code)
	}
}

func TestShedding_ReadyPassesThrough(t *testing.T) {
	r := probes.NewReadiness() // starts ready=true
	h := mw.Shedding(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 when ready, got %d", rr.Code)
	}
}

func TestShedding_PressureDropReturns200(t *testing.T) {
	r := probes.NewReadiness()
	h := mw.Shedding(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Simulate high pressure: readiness cleared → 503.
	r.Set(false)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 under pressure, got %d", rr.Code)
	}

	// Simulate pressure drop: readiness restored → 200.
	r.Set(true)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 after pressure drop, got %d", rr.Code)
	}
}
