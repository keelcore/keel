//go:build no_fips

package version

func init() { activeTags = append(activeTags, "no_fips") }