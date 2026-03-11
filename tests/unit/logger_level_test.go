package unit

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// ParseLevel
// ---------------------------------------------------------------------------

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
		lvl, err := logging.ParseLevel(tc.input)
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
		if _, err := logging.ParseLevel(s); err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", s, err)
		}
	}
}

func TestParseLevel_Unknown_ReturnsError(t *testing.T) {
	_, err := logging.ParseLevel("trace")
	if err == nil {
		t.Error("expected error for unknown level, got nil")
	}
}

// ---------------------------------------------------------------------------
// Level filtering
// ---------------------------------------------------------------------------

func TestLogger_LevelFilter_Debug_AllMessages(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Level: "debug", Out: &buf})
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
	log := logging.New(logging.Config{JSON: true, Level: "warn", Out: &buf})
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
	log := logging.New(logging.Config{JSON: true, Level: "error", Out: &buf})
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

// ---------------------------------------------------------------------------
// Reconfigure
// ---------------------------------------------------------------------------

func TestLogger_Reconfigure_ChangesLevel(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Level: "error", Out: &buf})

	// At error level, debug is suppressed.
	log.Debug("before_reconfig", nil)
	if strings.Contains(buf.String(), "before_reconfig") {
		t.Fatal("debug should be suppressed at error level")
	}

	// Reconfigure to debug.
	if err := log.Reconfigure(logging.Config{Level: "debug"}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	log.Debug("after_reconfig", nil)
	if !strings.Contains(buf.String(), "after_reconfig") {
		t.Error("debug should be visible after reconfiguring to debug level")
	}
}

func TestLogger_Reconfigure_InvalidLevel_PreservesOld(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Level: "warn", Out: &buf})

	err := log.Reconfigure(logging.Config{Level: "trace"})
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
	log := logging.New(logging.Config{JSON: true, Level: "info", Out: &buf})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = log.Reconfigure(logging.Config{Level: "debug"})
		}()
		go func() {
			defer wg.Done()
			log.Info("concurrent_log", nil)
		}()
	}
	wg.Wait()
}
