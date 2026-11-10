package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    int32
		wantErr bool
	}{
		{"debug", levelDebug, false},
		{"DEBUG", levelDebug, false},
		{"info", levelInfo, false},
		{"", levelInfo, false},
		{"warn", levelWarn, false},
		{"error", levelError, false},
		{"bogus", levelInfo, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if got != c.want {
			t.Errorf("ParseLevel(%q) level = %d, want %d", c.in, got, c.want)
		}
		if (err != nil) != c.wantErr {
			t.Errorf("ParseLevel(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
		}
	}
}

func TestNewDefaultOut(t *testing.T) {
	// Out nil → os.Stdout branch.
	l := New(Config{Level: "warn"})
	if l.out == nil {
		t.Fatal("expected default os.Stdout writer")
	}
	if l.level != levelWarn {
		t.Fatalf("level = %d, want %d", l.level, levelWarn)
	}
}

func TestNewWithOut(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{JSON: true, Level: "debug", Out: &buf})
	if l.out != &buf {
		t.Fatal("expected provided writer to be used")
	}
	if !l.json {
		t.Fatal("expected json true")
	}
}

func TestReconfigureError(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "info", Out: &buf})
	if err := l.Reconfigure(Config{Level: "nope"}); err == nil {
		t.Fatal("expected error for bad level")
	}
	// Previous level preserved.
	if l.level != levelInfo {
		t.Fatalf("level changed on error: %d", l.level)
	}
}

func TestReconfigureSuccessNilOut(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "info", Out: &buf})
	if err := l.Reconfigure(Config{Level: "error", JSON: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.level != levelError {
		t.Fatalf("level = %d, want %d", l.level, levelError)
	}
	if !l.json {
		t.Fatal("json flag not applied")
	}
	// Out nil on reload → writer preserved.
	if l.out != &buf {
		t.Fatal("out writer replaced when cfg.Out was nil")
	}
}

func TestReconfigureReplaceOut(t *testing.T) {
	var a, b bytes.Buffer
	l := New(Config{Level: "info", Out: &a})
	if err := l.Reconfigure(Config{Level: "warn", Out: &b}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.out != &b {
		t.Fatal("out writer not replaced when cfg.Out non-nil")
	}
}

func TestLevelFilterBlocksAndPasses(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "warn", Out: &buf})

	// Below threshold → filtered out.
	l.Debug("d", nil)
	l.Info("i", nil)
	if buf.Len() != 0 {
		t.Fatalf("expected filtered output, got %q", buf.String())
	}

	// At/above threshold → emitted.
	l.Warn("w", nil)
	l.Error("e", map[string]any{"k": "v"})
	out := buf.String()
	if !strings.Contains(out, "w") || !strings.Contains(out, "e") {
		t.Fatalf("expected warn+error output, got %q", out)
	}
}

func TestWriteTextFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "debug", Out: &buf})
	l.Info("hello", map[string]any{"a": 1})
	out := buf.String()
	if !strings.Contains(out, "[info]") || !strings.Contains(out, "hello") {
		t.Fatalf("unexpected text output: %q", out)
	}
}

func TestWriteJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{JSON: true, Level: "debug", Out: &buf})
	l.Info("hello", nil) // nil fields → map allocated inside write
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output not JSON: %v (%q)", err, buf.String())
	}
	if m["msg"] != "hello" || m["level"] != "info" {
		t.Fatalf("unexpected json fields: %v", m)
	}
	if _, ok := m["ts"]; !ok {
		t.Fatal("missing ts field")
	}
}

func TestExit(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "error", Out: &buf}) // level filter would block info
	var code int
	called := false
	l.ExitFn = func(c int) { code = c; called = true }
	l.Exit("bye", nil)
	if !called || code != 0 {
		t.Fatalf("Exit did not call ExitFn(0): called=%v code=%d", called, code)
	}
	if !strings.Contains(buf.String(), "bye") {
		t.Fatalf("Exit did not log (bypassing filter): %q", buf.String())
	}
}

func TestFatal(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "debug", Out: &buf})
	var code int
	called := false
	l.ExitFn = func(c int) { code = c; called = true }
	l.Fatal("dead", map[string]any{"x": 1})
	if !called || code != 1 {
		t.Fatalf("Fatal did not call ExitFn(1): called=%v code=%d", called, code)
	}
	if !strings.Contains(buf.String(), "dead") {
		t.Fatalf("Fatal did not log: %q", buf.String())
	}
}

// Text mode escapes control characters: a decoded %0a in a logged path cannot
// start a forged log line.
func TestTextMode_EscapesControlCharacters(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{JSON: false, Out: &buf})
	l.Info("access", map[string]any{"path": "/x\n2026-01-01 [error] forged\x1b[0m"})
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("log entry spans multiple lines: %q", out)
	}
	if !strings.Contains(out, `/x\n2026-01-01 [error] forged\x1b[0m`) {
		t.Fatalf("control characters not escaped: %q", out)
	}
	if escapeControl("plain") != "plain" {
		t.Fatal("plain text altered")
	}
}

// failRename makes every rotation rename fail, without touching the file: Windows cannot remove an open one.
func failRename(t *testing.T) error {
	t.Helper()
	boom := errors.New("rename boom")
	orig := rotateRename
	rotateRename = func(string, string) error { return boom }
	t.Cleanup(func() { rotateRename = orig })
	return boom
}

// A rotation that fails leaves the write in the current file.
func TestRotatingFile_WriteSurvivesAFailedRotation(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	write(t, r, "first\n")
	failRename(t)
	write(t, r, strings.Repeat("x", 1024*1024))
	if got := backups(t, path); len(got) != 0 {
		t.Fatalf("backups = %v, want none", got)
	}
}

func TestNewRotatingFile_OpenError(t *testing.T) {
	if _, err := NewRotatingFile(filepath.Join(t.TempDir(), "missing", "keel.log"), RotationPolicy{}); err == nil {
		t.Fatal("want an error for a path in a missing directory")
	}
}
