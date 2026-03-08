// tests/unit/logging_gaps_test.go
package unit

import (
	"bytes"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// Logger.Error
// ---------------------------------------------------------------------------

func TestLogger_Error_WritesErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	log.Error("something_failed", map[string]any{"code": 42})

	out := buf.String()
	if !strings.Contains(out, `"level":"error"`) {
		t.Errorf("expected level=error in log output, got: %s", out)
	}
	if !strings.Contains(out, "something_failed") {
		t.Errorf("expected msg in log output, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// Non-JSON (text) format
// ---------------------------------------------------------------------------

func TestLogger_TextFormat_ContainsLevelAndMsg(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: false, Out: &buf})
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
	log := logging.New(logging.Config{JSON: false, Out: &buf})
	log.Warn("warn_event", nil) // nil fields → defaults to empty map

	out := buf.String()
	if !strings.Contains(out, "[warn]") {
		t.Errorf("expected [warn] in text output, got: %s", out)
	}
}