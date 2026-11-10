//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

// A rotation that fails is warned about. Removing the open file makes the rename fail, which only
// POSIX systems allow.
func TestRotateLogs_FailureWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keel.log")
	s, _ := fileSinkServer(t, path)
	s.applyRemoteSink(s.cfg)
	s.logger.Info("line", nil)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.rotateLogs()
	if !logged(t, path, "log_rotation_failed") {
		t.Fatal("want the failure logged")
	}
}
