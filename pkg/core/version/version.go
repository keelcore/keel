package version

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
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

// fipsRuntimeActive returns true when the FIPS runtime mode is active.
// It mirrors the logic in pkg/core/fips.Check without creating a package dependency.
// fipsBuild is injected by the caller (typically the build-tag constant fipsBuilt) so
// that tests can exercise every branch without depending on a specific build tag.
func fipsRuntimeActive(fipsBuild bool) bool {
	if !fipsBuild {
		return false
	}
	if v := os.Getenv("GOFIPS140"); v != "" {
		return true
	}
	for _, token := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if strings.TrimSpace(token) == "fips140=only" {
			return true
		}
	}
	return false
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
