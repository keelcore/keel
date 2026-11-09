package knobs

import (
	"regexp"
	"runtime/debug"
	"sort"
)

// readBuildInfo is the binary's build information; tests replace it.
var readBuildInfo = debug.ReadBuildInfo

// ownerVersion is the version of the owning module this binary links, or "" when the build
// information does not name it.
func ownerVersion(owner string) string {
	info, ok := readBuildInfo()
	if !ok || owner == "" {
		return ""
	}
	if info.Main.Path == owner {
		return info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path == owner {
			return d.Version
		}
	}
	return ""
}

// ownerRef is "path@version" for a module's owner, "path" when its version is unknown, or "".
func ownerRef(m *module) string {
	if m == nil || m.owner == "" {
		return ""
	}
	if v := ownerVersion(m.owner); v != "" {
		return m.owner + "@" + v
	}
	return m.owner
}

var knobsModulePath = regexp.MustCompile(`^github\.com/keelcore/knobs(/v[0-9]+)?$`)

// Majors lists the distinct major versions of this module among module paths (github.com/keelcore/knobs,
// github.com/keelcore/knobs/v2, ...), sorted. Go's minimal version selection keeps one version per major
// path, so more than one entry means one binary links two incompatible knobs engines.
func Majors(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if knobsModulePath.MatchString(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
