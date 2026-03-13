// pkg/core/version/version_test.go
package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// fipsRuntimeActive
//
// fipsBuild is an injected parameter, so every branch is reachable regardless
// of whether the binary was compiled with or without the fips build tag.
// ---------------------------------------------------------------------------

// fipsRuntimeActive returns false immediately when fipsBuild is false,
// even if GOFIPS140 or GODEBUG would otherwise indicate FIPS mode.
func TestFIPSRuntimeActive_FipsBuiltFalse_ShortCircuits(t *testing.T) {
	old := os.Getenv("GOFIPS140")
	os.Setenv("GOFIPS140", "1")
	defer os.Setenv("GOFIPS140", old)

	if fipsRuntimeActive(false) {
		t.Error("expected false when fipsBuild=false, regardless of env vars")
	}
}

// fipsRuntimeActive returns false when fipsBuild is true but neither
// GOFIPS140 nor GODEBUG fips140=only is set.
func TestFIPSRuntimeActive_NoEnvVars(t *testing.T) {
	old1 := os.Getenv("GOFIPS140")
	old2 := os.Getenv("GODEBUG")
	os.Setenv("GOFIPS140", "")
	os.Setenv("GODEBUG", "")
	defer func() {
		os.Setenv("GOFIPS140", old1)
		os.Setenv("GODEBUG", old2)
	}()

	if fipsRuntimeActive(true) {
		t.Error("expected false when GOFIPS140 and GODEBUG are empty")
	}
}

// fipsRuntimeActive returns true when GOFIPS140 is non-empty and fipsBuild is true.
func TestFIPSRuntimeActive_GOFIPS140Set(t *testing.T) {
	old := os.Getenv("GOFIPS140")
	os.Setenv("GOFIPS140", "1")
	defer os.Setenv("GOFIPS140", old)

	if !fipsRuntimeActive(true) {
		t.Error("expected true when GOFIPS140=1 and fipsBuild=true")
	}
}

// fipsRuntimeActive returns true when GODEBUG contains fips140=only and fipsBuild is true.
func TestFIPSRuntimeActive_GODEBUGFips(t *testing.T) {
	oldGOFIPS := os.Getenv("GOFIPS140")
	oldGODEBUG := os.Getenv("GODEBUG")
	os.Setenv("GOFIPS140", "")
	os.Setenv("GODEBUG", "fips140=only")
	defer func() {
		os.Setenv("GOFIPS140", oldGOFIPS)
		os.Setenv("GODEBUG", oldGODEBUG)
	}()

	if !fipsRuntimeActive(true) {
		t.Error("expected true when GODEBUG=fips140=only and fipsBuild=true")
	}
}

// fipsRuntimeActive returns true when GODEBUG contains fips140=only among
// multiple comma-separated tokens (exercises the split-and-scan loop).
func TestFIPSRuntimeActive_GODEBUGMultiToken(t *testing.T) {
	oldGOFIPS := os.Getenv("GOFIPS140")
	oldGODEBUG := os.Getenv("GODEBUG")
	os.Setenv("GOFIPS140", "")
	os.Setenv("GODEBUG", "http2debug=1,fips140=only,netdns=go")
	defer func() {
		os.Setenv("GOFIPS140", oldGOFIPS)
		os.Setenv("GODEBUG", oldGODEBUG)
	}()

	if !fipsRuntimeActive(true) {
		t.Error("expected true when GODEBUG multi-token string contains fips140=only")
	}
}

// fipsRuntimeActive trims whitespace from each GODEBUG token before comparing.
func TestFIPSRuntimeActive_GODEBUGTokenWhitespaceTrimmed(t *testing.T) {
	oldGOFIPS := os.Getenv("GOFIPS140")
	oldGODEBUG := os.Getenv("GODEBUG")
	os.Setenv("GOFIPS140", "")
	os.Setenv("GODEBUG", "http2debug=1, fips140=only")
	defer func() {
		os.Setenv("GOFIPS140", oldGOFIPS)
		os.Setenv("GODEBUG", oldGODEBUG)
	}()

	if !fipsRuntimeActive(true) {
		t.Error("expected true when fips140=only token has leading whitespace")
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

// Get returns an Info with non-empty Version and GoVersion fields.
func TestGet_ReturnsInfo(t *testing.T) {
	info := Get()
	if info.Version == "" {
		t.Error("expected non-empty Version")
	}
	if info.GoVersion == "" {
		t.Error("expected non-empty GoVersion")
	}
}

// Get.BuildTags is a non-nil slice (may be empty but never nil).
func TestGet_BuildTagsNonNil(t *testing.T) {
	info := Get()
	if info.BuildTags == nil {
		t.Error("expected non-nil BuildTags slice")
	}
}

// Get — override the ldflags variables to confirm VCS branch is NOT taken
// when they are already set to non-"unknown" values.
func TestGet_OverriddenLDFlags(t *testing.T) {
	old1, old2 := Version, Commit
	Version = "1.2.3"
	Commit = "abc123"
	defer func() {
		Version = old1
		Commit = old2
	}()
	info := Get()
	if info.Version != "1.2.3" {
		t.Errorf("expected Version=1.2.3, got %q", info.Version)
	}
	// Commit was set to non-"unknown" so VCS revision is not applied.
	if info.Commit != "abc123" {
		t.Errorf("expected Commit=abc123, got %q", info.Commit)
	}
}

// Get — set Commit and BuildDate to "unknown" so that VCS settings are applied.
func TestGet_UnknownCommitUseVCS(t *testing.T) {
	old1, old2 := Commit, BuildDate
	Commit = "unknown"
	BuildDate = "unknown"
	defer func() {
		Commit = old1
		BuildDate = old2
	}()
	info := Get()
	// Whether VCS metadata is present or not, Get must not panic and
	// must return a non-empty GoVersion.
	if info.GoVersion == "" {
		t.Error("expected non-empty GoVersion")
	}
}

// Get round-trips to JSON without error.
func TestGet_JSONRoundTrip(t *testing.T) {
	info := Get()
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded Info
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Version != info.Version {
		t.Errorf("Version mismatch after round-trip: got %q, want %q", decoded.Version, info.Version)
	}
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// Handler returns 200 with application/json content-type.
func TestHandler_Returns200JSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("content-type")
	if ct != "application/json" {
		t.Errorf("expected content-type application/json, got %q", ct)
	}
}

// Handler response decodes to a valid Info struct.
func TestHandler_ResponseDecodesInfo(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	Handler().ServeHTTP(rr, req)

	var info Info
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.GoVersion == "" {
		t.Error("expected non-empty GoVersion in handler response")
	}
}
