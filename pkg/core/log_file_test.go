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

// logging.file: every entry reaches the file (and stdout, untouched here).
func TestFileSink_ReceivesLogLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keel.log")
	s, _ := fileSinkServer(t, path)
	s.applyRemoteSink(s.cfg)
	s.logger.Info("to_the_file", nil)
	if !logged(t, path, "to_the_file") {
		t.Fatal("line not in the file sink")
	}
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

// A path that cannot open: warned, and logging continues on stdout.
func TestFileSink_OpenFailureWarns(t *testing.T) {
	s, _ := fileSinkServer(t, filepath.Join(t.TempDir(), "missing", "keel.log"))
	var out bytes.Buffer
	s.logger = logging.New(logging.Config{JSON: true, Out: &out})
	s.applyRemoteSink(s.cfg)
	if !strings.Contains(out.String(), "log_file_open_failed") {
		t.Fatalf("log = %q", out.String())
	}
}

// SIGHUP: the same path keeps its file with the new policy; a new path switches; empty detaches.
func TestFileSink_Reload(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	s, _ := fileSinkServer(t, a)
	s.applyRemoteSink(s.cfg)
	first := s.fileSink
	cfg := s.cfg
	cfg.Logging.Rotation.MaxBackups = 1
	s.applyRemoteSink(cfg)
	if s.fileSink != first {
		t.Fatal("the same path must keep the open file")
	}
	cfg.Logging.File = b
	s.applyRemoteSink(cfg)
	s.logger.Info("in_b", nil)
	if !logged(t, b, "in_b") {
		t.Fatal("new path not used")
	}
	cfg.Logging.File = ""
	s.applyRemoteSink(cfg)
	if s.fileSink != nil {
		t.Fatal("empty path must detach the file sink")
	}
}
