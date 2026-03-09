package unit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/pkg/core/version"
)

// ---------------------------------------------------------------------------
// /version
// ---------------------------------------------------------------------------

func TestVersion_ReturnsJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	version.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/version", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("content-type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestVersion_ContainsRequiredFields(t *testing.T) {
	rr := httptest.NewRecorder()
	version.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/version", nil))

	var info version.Info
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.GoVersion == "" {
		t.Error("go_version must be non-empty")
	}
	if info.Version == "" {
		t.Error("version must be non-empty")
	}
	if info.BuildTags == nil {
		t.Error("build_tags must not be nil (use empty slice)")
	}
}

// ---------------------------------------------------------------------------
// /startupz
// ---------------------------------------------------------------------------

func TestStartupz_Returns503BeforeDone(t *testing.T) {
	s := probes.NewStartup()
	mux := http.NewServeMux()
	probes.RegisterStartup(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/startupz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 before Done()", rr.Code)
	}
}

func TestStartupz_Returns200AfterDone(t *testing.T) {
	s := probes.NewStartup()
	s.Done()
	mux := http.NewServeMux()
	probes.RegisterStartup(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/startupz", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 after Done()", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// /readyz with named checks
// ---------------------------------------------------------------------------

func TestReadiness_FailedCheckReturns503WithName(t *testing.T) {
	r := probes.NewReadiness()
	r.AddCheck("db", func() error { return errors.New("connection refused") })

	mux := http.NewServeMux()
	probes.RegisterReady(mux, r)

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
	r := probes.NewReadiness()
	r.AddCheck("db", func() error { return nil })
	r.AddCheck("cache", func() error { return nil })

	mux := http.NewServeMux()
	probes.RegisterReady(mux, r)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when all checks pass", rr.Code)
	}
}

func TestReadiness_AtomicFalseOverridesPassingChecks(t *testing.T) {
	r := probes.NewReadiness()
	r.Set(false) // e.g. backpressure
	r.AddCheck("db", func() error { return nil })

	mux := http.NewServeMux()
	probes.RegisterReady(mux, r)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when atomic ready=false", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// /health/fips (P16)
// ---------------------------------------------------------------------------

func TestFIPSHealth_ReturnsJSON(t *testing.T) {
	mux := http.NewServeMux()
	probes.RegisterFIPS(mux)

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

// ---------------------------------------------------------------------------
// /debug/pprof/ on admin mux only
// ---------------------------------------------------------------------------

func TestPProf_RegisteredOnAdminMuxReturns200(t *testing.T) {
	adminMux := http.NewServeMux()
	probes.RegisterPProf(adminMux)

	rr := httptest.NewRecorder()
	adminMux.ServeHTTP(rr, httptest.NewRequest("GET", "/debug/pprof/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("pprof on admin: status = %d, want 200", rr.Code)
	}
}

func TestPProf_NotOnMainHandlerReturns404(t *testing.T) {
	// Main mux has no pprof routes registered.
	mainMux := http.NewServeMux()

	rr := httptest.NewRecorder()
	mainMux.ServeHTTP(rr, httptest.NewRequest("GET", "/debug/pprof/", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("pprof on main: status = %d, want 404", rr.Code)
	}
}
