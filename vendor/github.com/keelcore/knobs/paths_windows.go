//go:build windows

package knobs

import (
	"os"
	"path/filepath"
)

// DefaultGlobalPath is the global file's default location: %ProgramData%\<app>\config.yaml, read from the
// process environment.
func DefaultGlobalPath(app string) string {
	return defaultGlobalPath(app, defaultFileName, os.LookupEnv)
}

func defaultGlobalPath(app, name string, env func(string) (string, bool)) string {
	root := `C:\ProgramData`
	if p, ok := env("ProgramData"); ok && p != "" {
		root = p
	}
	return filepath.Join(root, app, name)
}

// DefaultUserPath is the user file's default location: %APPDATA%\<app>\config.yaml, else none.
func DefaultUserPath(app string, env func(string) (string, bool)) string {
	return defaultUserPath(app, defaultFileName, env)
}

func defaultUserPath(app, name string, env func(string) (string, bool)) string {
	if a, ok := env("APPDATA"); ok && a != "" {
		return filepath.Join(a, app, name)
	}
	return ""
}
