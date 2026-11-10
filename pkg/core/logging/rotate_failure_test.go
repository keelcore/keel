package logging

import (
	"io/fs"
	"os"
	"path/filepath"
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

// A backup that cannot be removed is reported, after the rest are handled.
func TestPrune_ReportsARemovalFailure(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	if err := os.MkdirAll(filepath.Join(path+".1", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".2", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.prune(RotationPolicy{MaxBackups: 1}); err == nil {
		t.Fatal("want an error")
	}
}

func TestPrune_SkipsTemporaryFiles(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	if err := os.WriteFile(path+".1.gz.tmp", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.prune(RotationPolicy{MaxBackups: 0, MaxAgeDays: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1.gz.tmp"); err != nil {
		t.Fatal(err)
	}
}
