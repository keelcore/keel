package version

import (
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
	Version   string   `json:"version"`
	Commit    string   `json:"commit"`
	BuildDate string   `json:"build_date"`
	GoVersion string   `json:"go_version"`
	BuildTags []string `json:"build_tags"`
}

// Get returns the current build info, enriching commit/date from VCS metadata
// embedded by the toolchain when -ldflags overrides are absent.
func Get() Info {
	commit := Commit
	buildDate := BuildDate
	if info, ok := debug.ReadBuildInfo(); ok {
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
		Version:   Version,
		Commit:    commit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		BuildTags: tags,
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
