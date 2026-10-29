package logging

import (
	"bytes"
	"strings"
	"testing"
)

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
