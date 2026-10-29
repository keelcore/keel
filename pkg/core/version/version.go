package version

import (
	"crypto/fips140"
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"
)

// Build-time overrides via -ldflags "-X github.com/keelcore/keel/pkg/core/version.Version=1.2.3".
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// activeTags is populated by build-tag-specific init() functions.
var activeTags []string

// Info is the JSON schema for GET /version.
type Info struct {
	Version    string   `json:"version"`
	Commit     string   `json:"commit"`
	BuildDate  string   `json:"build_date"`
	GoVersion  string   `json:"go_version"`
	BuildTags  []string `json:"build_tags"`
	FIPSActive bool     `json:"fips_active"`
}

// fipsModuleEnabled is a seam over crypto/fips140.Enabled for tests.
var fipsModuleEnabled = fips140.Enabled

// fipsRuntimeActive returns true when this is a FIPS build AND the Go
// Cryptographic Module reports FIPS 140 mode (crypto/fips140.Enabled), matching
// pkg/core/fips.Check without a package dependency. Environment variables are
// not consulted: GOFIPS140 only matters at build time (GOFIPS140=off is
// non-empty) and GODEBUG is read at process start. fipsBuild is injected by the
// caller (typically the build-tag constant fipsBuilt) so tests reach every branch.
func fipsRuntimeActive(fipsBuild bool) bool {
	return fipsBuild && fipsModuleEnabled()
}

// Get returns the current build info, enriching commit/date from VCS metadata
// embedded by the toolchain when -ldflags overrides are absent.
// It delegates to getInfo so tests can inject controlled build metadata.
func Get() Info {
	return getInfo(debug.ReadBuildInfo())
}

// getInfo assembles an Info from the package-level ldflags variables and the
// provided VCS build metadata. ok=false (e.g. no embedded VCS info) is safe:
// the loop is skipped and ldflags values are used as-is. Commit and BuildDate
// are only overridden by VCS data when they still hold the default "unknown"
// sentinel, preserving any -ldflags override.
func getInfo(info *debug.BuildInfo, ok bool) Info {
	commit := Commit
	buildDate := BuildDate
	if ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "unknown" {
					commit = s.Value
				}
			case "vcs.time":
				if buildDate == "unknown" {
					buildDate = s.Value
				}
			}
		}
	}
	tags := make([]string, len(activeTags))
	copy(tags, activeTags)
	return Info{
		Version:    Version,
		Commit:     commit,
		BuildDate:  buildDate,
		GoVersion:  runtime.Version(),
		BuildTags:  tags,
		FIPSActive: fipsRuntimeActive(fipsBuilt),
	}
}

// Handler returns an http.Handler for GET /version.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Get())
	})
}
