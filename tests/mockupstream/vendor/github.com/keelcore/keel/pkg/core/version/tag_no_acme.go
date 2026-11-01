//go:build no_acme

package version

func init() { activeTags = append(activeTags, "no_acme") }
