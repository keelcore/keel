//go:build no_statsd

package version

func init() { activeTags = append(activeTags, "no_statsd") }