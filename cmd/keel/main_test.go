package main

import (
	"flag"
	"os"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// exitSentinel is the panic value used to intercept log.ExitFn calls in tests.
type exitSentinel struct{ code int }

// newTrappedLogger returns a Logger whose ExitFn panics with exitSentinel
// instead of calling os.Exit, allowing tests to intercept terminal actions.
func newTrappedLogger() *logging.Logger {
	l := logging.New(logging.Config{JSON: true})
	l.ExitFn = func(code int) { panic(exitSentinel{code}) }
	return l
}

// resetFlags resets the global flag values registered by pkg/clisupport to
// their defaults so each test starts from a clean state.
func resetFlags(t *testing.T) {
	t.Helper()
	for _, name := range []string{"version", "validate", "check-integrity", "check-shred"} {
		f := flag.CommandLine.Lookup(name)
		if f != nil {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatalf("resetFlags: cannot reset flag %q: %v", name, err)
			}
		}
	}
}

func TestRun_Version(t *testing.T) {
	resetFlags(t)
	os.Args = []string{"keel", "--version"}
	log := newTrappedLogger()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from ExitFn")
		}
		s, ok := r.(exitSentinel)
		if !ok {
			panic(r)
		}
		if s.code != 0 {
			t.Errorf("expected exit code 0, got %d", s.code)
		}
	}()

	run(log)
}

func TestRun_Validate(t *testing.T) {
	resetFlags(t)
	os.Args = []string{"keel", "--validate"}
	log := newTrappedLogger()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from ExitFn")
		}
		s, ok := r.(exitSentinel)
		if !ok {
			panic(r)
		}
		if s.code != 0 {
			t.Errorf("expected exit code 0, got %d", s.code)
		}
	}()

	run(log)
}

func TestRun_CheckShred(t *testing.T) {
	resetFlags(t)
	os.Args = []string{"keel", "--check-shred"}
	log := newTrappedLogger()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from ExitFn")
		}
		s, ok := r.(exitSentinel)
		if !ok {
			panic(r)
		}
		if s.code != 0 {
			t.Errorf("expected exit code 0, got %d", s.code)
		}
	}()

	run(log)
}

func TestRun_ServerStartup(t *testing.T) {
	resetFlags(t)
	os.Args = []string{"keel"}
	t.Setenv("KEEL_TEST_SHUTDOWN_AFTER", "100ms")

	got := run(nil)
	if got != 0 {
		t.Errorf("expected return code 0, got %d", got)
	}
}
