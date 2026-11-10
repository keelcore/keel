package logging

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// failOpens makes rotateOpenFile run fn instead of opening, for names ending in suffix.
func failOpens(t *testing.T, suffix string, fn func(name string) (*os.File, error)) {
	t.Helper()
	orig := rotateOpenFile
	rotateOpenFile = func(name string, flag int, perm fs.FileMode) (*os.File, error) {
		if strings.HasSuffix(name, suffix) {
			return fn(name)
		}
		return orig(name, flag, perm)
	}
	t.Cleanup(func() { rotateOpenFile = orig })
}
