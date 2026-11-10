package probes

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessSetGet(t *testing.T) {
	r := NewReadiness()
	if !r.Get() {
		t.Fatalf("new readiness should default to ready")
	}
	r.Set(false)
	if r.Get() {
		t.Fatalf("readiness should report not-ready after Set(false)")
	}
	r.Set(true)
	if !r.Get() {
		t.Fatalf("readiness should report ready after Set(true)")
	}
}

func TestIsReadyBackpressure(t *testing.T) {
	r := NewReadiness()
	r.Set(false)
	ok, failing := r.IsReady()
	if ok {
		t.Fatalf("IsReady ok = true, want false under backpressure")
	}
	if len(failing) != 1 || failing[0] != "backpressure" {
		t.Fatalf("failing = %v, want [backpressure]", failing)
	}
}

func TestIsReadyChecksPassAndFail(t *testing.T) {
	r := NewReadiness()
	r.AddCheck("db", func() error { return nil })
	r.AddCheck("cache", func() error { return errors.New("down") })

	ok, failing := r.IsReady()
	if ok {
		t.Fatalf("IsReady ok = true, want false with a failing check")
	}
	if len(failing) != 1 || failing[0] != "cache: down" {
		t.Fatalf("failing = %v, want [cache: down]", failing)
	}
}

func TestIsReadyAllChecksPass(t *testing.T) {
	r := NewReadiness()
	r.AddCheck("db", func() error { return nil })

	ok, failing := r.IsReady()
	if !ok {
		t.Fatalf("IsReady ok = false, want true when all checks pass")
	}
	if len(failing) != 0 {
		t.Fatalf("failing = %v, want empty", failing)
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
