// pkg/core/version/version_test.go
package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"
)

// ---------------------------------------------------------------------------
// fipsRuntimeActive
//
// fipsBuild is injected and the crypto-module answer is stubbed, so every
// branch is reachable regardless of build tag or the test process's FIPS mode.
// ---------------------------------------------------------------------------

func stubFIPSModule(t *testing.T, enabled bool) {
	t.Helper()
	orig := fipsModuleEnabled
	fipsModuleEnabled = func() bool { return enabled }
	t.Cleanup(func() { fipsModuleEnabled = orig })
}

// A non-FIPS build is never FIPS-active, whatever the crypto module says.
func TestFIPSRuntimeActive_FipsBuiltFalse_ShortCircuits(t *testing.T) {
	stubFIPSModule(t, true)
	if fipsRuntimeActive(false) {
		t.Error("expected false when fipsBuild=false")
	}
}

// A FIPS build reports exactly what the crypto module reports.
func TestFIPSRuntimeActive_FollowsCryptoModule(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		stubFIPSModule(t, enabled)
		if got := fipsRuntimeActive(true); got != enabled {
			t.Errorf("fipsRuntimeActive(true) = %v with module enabled=%v", got, enabled)
		}
	}
}

// Environment variables cannot fake FIPS (GOFIPS140=off is non-empty).
func TestFIPSRuntimeActive_EnvCannotFakeFIPS(t *testing.T) {
	stubFIPSModule(t, false)
	t.Setenv("GOFIPS140", "off")
	t.Setenv("GODEBUG", "fips140=only")
	if fipsRuntimeActive(true) {
		t.Error("fipsRuntimeActive trusted environment variables")
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
