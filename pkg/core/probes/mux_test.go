package probes

import (
	"encoding/json"
	"errors"
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

func TestReadiness_FailedCheckReturns503WithName(t *testing.T) {
	r := NewReadiness()
	r.AddCheck("db", func() error { return errors.New("connection refused") })

	mux := http.NewServeMux()
	RegisterReady(mux, r)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "db") {
		t.Errorf("body %q does not mention check name 'db'", body)
	}
}

func TestReadiness_AllChecksPassReturn200(t *testing.T) {
	r := NewReadiness()
	r.AddCheck("db", func() error { return nil })
	r.AddCheck("cache", func() error { return nil })

	mux := http.NewServeMux()
	RegisterReady(mux, r)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when all checks pass", rr.Code)
	}
}

func TestReadiness_AtomicFalseOverridesPassingChecks(t *testing.T) {
	r := NewReadiness()
	r.Set(false) // e.g. backpressure
	r.AddCheck("db", func() error { return nil })

	mux := http.NewServeMux()
	RegisterReady(mux, r)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when atomic ready=false", rr.Code)
	}
}

func TestFIPSHealth_ReturnsJSON(t *testing.T) {
	mux := http.NewServeMux()
	RegisterFIPS(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/health/fips", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("content-type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var resp map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["fips_active"]; !ok {
		t.Error("response missing 'fips_active' field")
	}
}
