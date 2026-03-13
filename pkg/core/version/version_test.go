// pkg/core/version/version_test.go
package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/debug"
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
// Get / getInfo
//
// Get is a thin wrapper around getInfo(debug.ReadBuildInfo()), so smoke tests
// here verify the public surface. Branch coverage of the VCS logic is handled
// by the getInfo tests below, which inject controlled *debug.BuildInfo values.
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

// ---------------------------------------------------------------------------
// getInfo — VCS injection tests
// ---------------------------------------------------------------------------

// getInfo with ok=false skips the VCS loop and returns ldflags values as-is.
func TestGetInfo_OkFalse_UsesLDFlags(t *testing.T) {
	old1, old2 := Commit, BuildDate
	Commit = "ldcommit"
	BuildDate = "lddate"
	defer func() {
		Commit = old1
		BuildDate = old2
	}()
	info := getInfo(nil, false)
	if info.Commit != "ldcommit" {
		t.Errorf("expected Commit=ldcommit, got %q", info.Commit)
	}
	if info.BuildDate != "lddate" {
		t.Errorf("expected BuildDate=lddate, got %q", info.BuildDate)
	}
}

// getInfo with ok=true and Commit=="unknown" replaces commit from vcs.revision.
func TestGetInfo_VCSRevision_OverridesUnknown(t *testing.T) {
	old := Commit
	Commit = "unknown"
	defer func() { Commit = old }()
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeef"}},
	}
	info := getInfo(bi, true)
	if info.Commit != "deadbeef" {
		t.Errorf("expected Commit=deadbeef, got %q", info.Commit)
	}
}

// getInfo with ok=true and Commit!="unknown" does not replace commit from vcs.revision.
func TestGetInfo_VCSRevision_DoesNotOverrideLDFlag(t *testing.T) {
	old := Commit
	Commit = "pinned"
	defer func() { Commit = old }()
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeef"}},
	}
	info := getInfo(bi, true)
	if info.Commit != "pinned" {
		t.Errorf("expected Commit=pinned, got %q", info.Commit)
	}
}

// getInfo with ok=true and BuildDate=="unknown" replaces build date from vcs.time.
func TestGetInfo_VCSTime_OverridesUnknown(t *testing.T) {
	old := BuildDate
	BuildDate = "unknown"
	defer func() { BuildDate = old }()
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2024-01-01T00:00:00Z"}},
	}
	info := getInfo(bi, true)
	if info.BuildDate != "2024-01-01T00:00:00Z" {
		t.Errorf("expected BuildDate=2024-01-01T00:00:00Z, got %q", info.BuildDate)
	}
}

// getInfo with ok=true and BuildDate!="unknown" does not replace it from vcs.time.
func TestGetInfo_VCSTime_DoesNotOverrideLDFlag(t *testing.T) {
	old := BuildDate
	BuildDate = "2099-12-31"
	defer func() { BuildDate = old }()
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2024-01-01T00:00:00Z"}},
	}
	info := getInfo(bi, true)
	if info.BuildDate != "2099-12-31" {
		t.Errorf("expected BuildDate=2099-12-31, got %q", info.BuildDate)
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
