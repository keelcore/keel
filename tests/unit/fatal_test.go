package unit

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// TestLogger_Fatal_LogsBeforeExit verifies that Fatal writes the log line
// before calling os.Exit(1). A subprocess is used because os.Exit cannot be
// intercepted in the same process.
func TestLogger_Fatal_LogsBeforeExit(t *testing.T) {
	if os.Getenv("TEST_LOGGER_FATAL") == "1" {
		// Subprocess path: call Fatal and let the process exit with code 1.
		log := logging.New(logging.Config{Out: os.Stderr})
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