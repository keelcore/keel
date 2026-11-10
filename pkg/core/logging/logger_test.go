package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
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

// TestLogger_Fatal_LogsBeforeExit verifies that Fatal writes the log line
// before calling os.Exit(1). A subprocess is used because os.Exit cannot be
// intercepted in the same process.
func TestLogger_Fatal_LogsBeforeExit(t *testing.T) {
	if os.Getenv("TEST_LOGGER_FATAL") == "1" {
		// Subprocess path: call Fatal and let the process exit with code 1.
		log := New(Config{Out: os.Stderr})
		log.Fatal("fatal-test-event", nil)
		return
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestLogger_Fatal_LogsBeforeExit$",
		"-test.v",
	)
	cmd.Env = append(os.Environ(), "TEST_LOGGER_FATAL=1")
	var out bytes.Buffer
	cmd.Stderr = &out

	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
	}
	if !strings.Contains(out.String(), "fatal-test-event") {
		t.Errorf("expected 'fatal-test-event' in stderr before exit, got: %q", out.String())
	}
}

func TestParseLevel_KnownLevels(t *testing.T) {
	cases := []struct {
		input string
		want  int32
	}{
		{"debug", 0},
		{"info", 1},
		{"warn", 2},
		{"error", 3},
		{"", 1}, // empty → info
	}
	for _, tc := range cases {
		lvl, err := ParseLevel(tc.input)
		if err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", tc.input, err)
		}
		if lvl != tc.want {
			t.Errorf("ParseLevel(%q) = %d, want %d", tc.input, lvl, tc.want)
		}
	}
}

func TestParseLevel_CaseInsensitive(t *testing.T) {
	for _, s := range []string{"DEBUG", "Info", "WARN", "Error"} {
		if _, err := ParseLevel(s); err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", s, err)
		}
	}
}

func TestParseLevel_Unknown_ReturnsError(t *testing.T) {
	_, err := ParseLevel("trace")
	if err == nil {
		t.Error("expected error for unknown level, got nil")
	}
}

func TestLogger_LevelFilter_Debug_AllMessages(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Level: "debug", Out: &buf})
	log.Debug("d", nil)
	log.Info("i", nil)
	log.Warn("w", nil)
	log.Error("e", nil)
	out := buf.String()
	for _, msg := range []string{"\"d\"", "\"i\"", "\"w\"", "\"e\""} {
		if !strings.Contains(out, msg) {
			t.Errorf("expected %s in debug-level output, got: %s", msg, out)
		}
	}
}

func TestLogger_LevelFilter_Warn_SuppressesDebugAndInfo(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Level: "warn", Out: &buf})
	log.Debug("should_not_appear", nil)
	log.Info("also_not_appear", nil)
	log.Warn("visible", nil)
	out := buf.String()
	if strings.Contains(out, "should_not_appear") {
		t.Error("debug message leaked through warn filter")
	}
	if strings.Contains(out, "also_not_appear") {
		t.Error("info message leaked through warn filter")
	}
	if !strings.Contains(out, "visible") {
		t.Error("warn message missing from output")
	}
}

func TestLogger_LevelFilter_Error_SuppressesAll(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Level: "error", Out: &buf})
	log.Debug("d", nil)
	log.Info("i", nil)
	log.Warn("w", nil)
	log.Error("e", nil)
	out := buf.String()
	if strings.Contains(out, "\"d\"") || strings.Contains(out, "\"i\"") || strings.Contains(out, "\"w\"") {
		t.Error("non-error messages leaked through error filter")
	}
	if !strings.Contains(out, "\"e\"") {
		t.Error("error message missing from error-level output")
	}
}

func TestLogger_Reconfigure_ChangesLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Level: "error", Out: &buf})

	// At error level, debug is suppressed.
	log.Debug("before_reconfig", nil)
	if strings.Contains(buf.String(), "before_reconfig") {
		t.Fatal("debug should be suppressed at error level")
	}

	// Reconfigure to debug.
	if err := log.Reconfigure(Config{Level: "debug"}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	log.Debug("after_reconfig", nil)
	if !strings.Contains(buf.String(), "after_reconfig") {
		t.Error("debug should be visible after reconfiguring to debug level")
	}
}

func TestLogger_Reconfigure_InvalidLevel_PreservesOld(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Level: "warn", Out: &buf})

	err := log.Reconfigure(Config{Level: "trace"})
	if err == nil {
		t.Fatal("expected error for unknown level")
	}

	// warn level should still be in effect; debug suppressed, warn visible.
	log.Debug("d", nil)
	log.Warn("w", nil)
	out := buf.String()
	if strings.Contains(out, "\"d\"") {
		t.Error("debug leaked after failed Reconfigure")
	}
	if !strings.Contains(out, "\"w\"") {
		t.Error("warn missing after failed Reconfigure")
	}
}

func TestLogger_Reconfigure_Concurrent_NoRace(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Level: "info", Out: &buf})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = log.Reconfigure(Config{Level: "debug"})
		}()
		go func() {
			defer wg.Done()
			log.Info("concurrent_log", nil)
		}()
	}
	wg.Wait()
}

func TestLogger_Error_WritesErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: true, Out: &buf})
	log.Error("something_failed", map[string]any{"code": 42})

	out := buf.String()
	if !strings.Contains(out, `"level":"error"`) {
		t.Errorf("expected level=error in log output, got: %s", out)
	}
	if !strings.Contains(out, "something_failed") {
		t.Errorf("expected msg in log output, got: %s", out)
	}
}

func TestLogger_TextFormat_ContainsLevelAndMsg(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: false, Out: &buf})
	log.Info("text_event", map[string]any{"k": "v"})

	out := buf.String()
	if !strings.Contains(out, "[info]") {
		t.Errorf("expected [info] in text output, got: %s", out)
	}
	if !strings.Contains(out, "text_event") {
		t.Errorf("expected msg in text output, got: %s", out)
	}
}

func TestLogger_TextFormat_NilFields(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{JSON: false, Out: &buf})
	log.Warn("warn_event", nil) // nil fields → defaults to empty map

	out := buf.String()
	if !strings.Contains(out, "[warn]") {
		t.Errorf("expected [warn] in text output, got: %s", out)
	}
}
