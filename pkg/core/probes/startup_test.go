package probes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartupGetAndDone(t *testing.T) {
	s := NewStartup()
	if s.Get() {
		t.Fatalf("new startup should report not-done")
	}
	s.Done()
	if !s.Get() {
		t.Fatalf("startup should report done after Done()")
	}
}

func TestRegisterStartupHandler(t *testing.T) {
	s := NewStartup()
	mux := http.NewServeMux()
	RegisterStartup(mux, s)

	// Before Done(): 503 starting.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/startupz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-Done code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "starting\n" {
		t.Fatalf("pre-Done body = %q, want %q", rec.Body.String(), "starting\n")
	}

	// After Done(): 200 started.
	s.Done()
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/startupz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("post-Done code = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "started\n" {
		t.Fatalf("post-Done body = %q, want %q", rec.Body.String(), "started\n")
	}
}
