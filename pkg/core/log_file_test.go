package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

func fileSinkServer(t *testing.T, path string) (*Server, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Logging.File = path
	s := NewServer(logging.New(logging.Config{JSON: true, Out: &out}), cfg)
	s.runCtx = context.Background()
	t.Cleanup(s.closeFileSink)
	return s, &out
}

func logged(t *testing.T, path, msg string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == msg {
			return true
		}
	}
	return false
}

// SIGUSR2 rotates a live file sink; the next line lands in a fresh file.
func TestRotateLogs_RotatesTheFileSink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keel.log")
	s, _ := fileSinkServer(t, path)
	s.applyRemoteSink(s.cfg)
	s.logger.Info("before_rotation", nil)
	s.rotateLogs()
	s.logger.Info("after_rotation", nil)
	s.closeFileSink()
	backups, _ := filepath.Glob(path + ".*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want 1", backups)
	}
	if !logged(t, path, "after_rotation") || logged(t, path, "before_rotation") {
		t.Fatal("the current file should hold only the lines after the rotation")
	}
}

// With no file sink, SIGUSR2 logs that there is nothing to rotate.
func TestRotateLogs_NoFileSink(t *testing.T) {
	var out bytes.Buffer
	s := NewServer(logging.New(logging.Config{JSON: true, Out: &out}), config.Defaults())
	s.rotateLogs()
	if !strings.Contains(out.String(), "log_rotation_no_file_sink") {
		t.Fatalf("log = %q", out.String())
	}
}
