package probes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterHealth(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealth(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "ok\n")
	}
}

func TestRegisterReadyOK(t *testing.T) {
	r := NewReadiness()
	mux := http.NewServeMux()
	RegisterReady(mux, r)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ready\n" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "ready\n")
	}
}

func TestRegisterReadyNotReady(t *testing.T) {
	r := NewReadiness()
	r.Set(false)
	mux := http.NewServeMux()
	RegisterReady(mux, r)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "not ready: backpressure") {
		t.Fatalf("body = %q, want to contain %q", rec.Body.String(), "not ready: backpressure")
	}
}

func TestRegisterFIPS(t *testing.T) {
	mux := http.NewServeMux()
	RegisterFIPS(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/fips", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("content-type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["fips_active"] != fipsActive {
		t.Fatalf("fips_active = %v, want %v", got["fips_active"], fipsActive)
	}
}

func TestRegisterPProf(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPProf(mux)

	// Registration succeeds and the index handler responds.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pprof index code = %d, want %d", rec.Code, http.StatusOK)
	}
}
