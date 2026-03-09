//go:build no_sidecar

package version

func init() { activeTags = append(activeTags, "no_sidecar") }
