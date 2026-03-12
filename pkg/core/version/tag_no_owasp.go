//go:build no_owasp

package version

func init() { activeTags = append(activeTags, "no_owasp") }