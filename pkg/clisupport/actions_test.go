// pkg/clisupport/actions_test.go
package clisupport

import (
	"io"
	"testing"

	keelconfig "github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// exitSentinel is panicked by the injected ExitFn so tests can recover it.
type exitSentinel struct{ code int }

// trapExit sets log.ExitFn to panic with an exitSentinel and returns the
// deferred recover function.  Usage:
//
//	code, ok := trapExit(log, func() { ... })
func trapExit(log *logging.Logger, fn func()) (code int, exited bool) {
	defer func() {
		if r := recover(); r != nil {
			if s, ok2 := r.(exitSentinel); ok2 {
				code = s.code
				exited = true
			}
		}
	}()
	log.ExitFn = func(c int) { panic(exitSentinel{c}) }
	fn()
	return 0, false
}

// ---------------------------------------------------------------------------
// tryValidateConfig — internal function, no --validate flag set.
// When flagValidate is false (the default), tryValidateConfig must return
// the loaded default config without calling log.Exit.
// ---------------------------------------------------------------------------

func TestTryValidateConfig_DefaultConfig(t *testing.T) {
	// Ensure flags are not set so the function returns normally.
	v := false
	flagValidate = &v

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := tryValidateConfig(log)

	// Verify we got a valid config back (non-zero listeners port).
	if cfg.Listeners.HTTP.Port == 0 {
		t.Error("expected non-zero HTTP port from default config")
	}
}

// ---------------------------------------------------------------------------
// TryValidateApp — exported wrapper; when flagValidate is false it must be a
// no-op (returns without calling log.Exit).
// ---------------------------------------------------------------------------

func TestTryValidateApp_NoopWhenFlagFalse(t *testing.T) {
	v := false
	flagValidate = &v

	log := logging.New(logging.Config{Out: io.Discard})
	// Must not panic or exit.
	TryValidateApp(log)
}

// ---------------------------------------------------------------------------
// tryCheckShred — only exercised when flagCheckShred is true, which calls
// log.Exit. We verify the tag-set construction logic by calling tryCheckShred
// with the flag false, which is a no-op.
// ---------------------------------------------------------------------------

func TestTryCheckShred_Noop(t *testing.T) {
	v := false
	flagCheckShred = &v

	log := logging.New(logging.Config{Out: io.Discard})
	// With flag=false, tryCheckShred must return without calling log.Exit.
	tryCheckShred(log)
}

// ---------------------------------------------------------------------------
// tryVersion — no-op when flag is false.
// ---------------------------------------------------------------------------

func TestTryVersion_Noop(t *testing.T) {
	v := false
	flagVersion = &v

	log := logging.New(logging.Config{Out: io.Discard})
	tryVersion(log)
}

// ---------------------------------------------------------------------------
// tryCheckIntegrity — no-op when flag is false.
// ---------------------------------------------------------------------------

func TestTryCheckIntegrity_Noop(t *testing.T) {
	v := false
	flagCheckIntegrity = &v

	log := logging.New(logging.Config{Out: io.Discard})
	// With flag=false, tryCheckIntegrity must return without calling log.Exit.
	tryCheckIntegrity(log)
}

// ---------------------------------------------------------------------------
// DefaultConfig returned by tryValidateConfig must pass Validate.
// ---------------------------------------------------------------------------

func TestTryValidateConfig_ReturnsValidConfig(t *testing.T) {
	v := false
	flagValidate = &v

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := tryValidateConfig(log)

	if err := keelconfig.Validate(cfg); err != nil {
		t.Errorf("tryValidateConfig returned config that fails Validate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// EXIT-path tests — flag=true causes log.Exit which calls ExitFn(0).
// We inject a panicking ExitFn and use recover to observe the call.
// ---------------------------------------------------------------------------

func TestTryVersion_ExitsWhenFlagTrue(t *testing.T) {
	v := true
	flagVersion = &v
	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { tryVersion(log) })
	if !exited {
		t.Error("expected tryVersion to call log.Exit when --version flag is true")
	}
}

func TestTryCheckIntegrity_ExitsWhenFlagTrue(t *testing.T) {
	v := true
	flagCheckIntegrity = &v
	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { tryCheckIntegrity(log) })
	if !exited {
		t.Error("expected tryCheckIntegrity to call log.Exit when --check-integrity flag is true")
	}
}

func TestTryCheckShred_ExitsWhenFlagTrue(t *testing.T) {
	v := true
	flagCheckShred = &v
	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { tryCheckShred(log) })
	if !exited {
		t.Error("expected tryCheckShred to call log.Exit when --check-shred flag is true")
	}
}

func TestTryValidateConfig_ExitsWhenFlagValidateTrue(t *testing.T) {
	v := true
	flagValidate = &v
	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { tryValidateConfig(log) })
	if !exited {
		t.Error("expected tryValidateConfig to call log.Exit when --validate flag is true")
	}
}

func TestTryValidateApp_ExitsWhenFlagTrue(t *testing.T) {
	v := true
	flagValidate = &v
	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { TryValidateApp(log) })
	if !exited {
		t.Error("expected TryValidateApp to call log.Exit when --validate flag is true")
	}
}
