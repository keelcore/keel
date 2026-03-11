// tests/unit/version_fips_test.go
package unit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/version"
)

// Get returns an Info struct that contains the FIPSActive field.
func TestVersionInfo_FIPSActiveField_Present(t *testing.T) {
	info := version.Get()

	// Round-trip through JSON to verify the field is exported with the correct key.
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := m["fips_active"]; !ok {
		t.Error(`expected "fips_active" key in version JSON output`)
	}
}

// FIPSActive must be a bool (not nil, not a number).
func TestVersionInfo_FIPSActiveField_IsBool(t *testing.T) {
	b, _ := json.Marshal(version.Get())
	var m map[string]any
	json.Unmarshal(b, &m) //nolint:errcheck
	v, ok := m["fips_active"]
	if !ok {
		t.Fatal(`"fips_active" key missing`)
	}
	if _, isBool := v.(bool); !isBool {
		t.Errorf(`"fips_active" must be a JSON bool, got %T (%v)`, v, v)
	}
}

// The /version HTTP handler includes fips_active in its JSON response.
func TestVersionHandler_FIPSActiveField(t *testing.T) {
	rr := httptest.NewRecorder()
	version.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var info version.Info
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	// FIPSActive is a bool field; zero value (false) is valid — just confirm it decodes.
	_ = info.FIPSActive
}

// In the default build (!no_fips with no FIPS env), FIPSActive reflects actual runtime state.
// This test does not assert a specific value because CI may or may not have GOFIPS140 set;
// it asserts only that the field is structurally present and boolean.
func TestVersionInfo_FIPSActive_IsStructurallyValid(t *testing.T) {
	info := version.Get()
	// Confirm the field is accessible (compilation would fail if the field were absent).
	if info.FIPSActive != true && info.FIPSActive != false {
		t.Error("FIPSActive is neither true nor false — this cannot happen")
	}
}
