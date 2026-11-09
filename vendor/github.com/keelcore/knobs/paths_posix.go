//go:build !windows

package knobs

import "path/filepath"

// DefaultGlobalPath is the global file's default location: /etc/<app>/config.yaml.
func DefaultGlobalPath(app string) string { return defaultGlobalPath(app, nil) }

func defaultGlobalPath(app string, _ func(string) (string, bool)) string {
	return filepath.Join("/etc", app, "config.yaml")
}

// DefaultUserPath is the user file's default location: $XDG_CONFIG_HOME/<app>/config.yaml, else
// $HOME/.config/<app>/config.yaml, else none.
func DefaultUserPath(app string, env func(string) (string, bool)) string {
	if x, ok := env("XDG_CONFIG_HOME"); ok && x != "" {
		return filepath.Join(x, app, "config.yaml")
	}
	if h, ok := env("HOME"); ok && h != "" {
		return filepath.Join(h, ".config", app, "config.yaml")
	}
	return ""
}
