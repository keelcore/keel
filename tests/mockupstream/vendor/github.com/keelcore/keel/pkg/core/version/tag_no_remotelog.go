//go:build no_remotelog

package version

func init() { activeTags = append(activeTags, "no_remotelog") }
