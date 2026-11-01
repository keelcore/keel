//go:build no_otel

package version

func init() { activeTags = append(activeTags, "no_otel") }
