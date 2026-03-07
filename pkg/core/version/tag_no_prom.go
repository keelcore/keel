//go:build no_prom

package version

func init() { activeTags = append(activeTags, "no_prom") }