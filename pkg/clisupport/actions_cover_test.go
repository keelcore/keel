// pkg/clisupport/actions_cover_test.go
// Additional coverage tests exercising the tag branches, the config-invalid
// fatal branch, and the exported flag-parsing entry points. Uses the shared
// trapExit / exitSentinel helpers from actions_test.go (same package).
package clisupport

import (
	"errors"
	"io"
	"testing"

	keelconfig "github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/version"
)

// ---------------------------------------------------------------------------
// tryVersion — tags branch (len(info.BuildTags) > 0). The versionGet seam
// injects a non-empty BuildTags slice so the "tags" field is populated.
// ---------------------------------------------------------------------------

func TestTryVersion_TagsBranch(t *testing.T) {
	v := true
	flagVersion = &v

	orig := versionGet
	versionGet = func() version.Info { return version.Info{BuildTags: []string{"fips", "no_h3"}} }
	t.Cleanup(func() { versionGet = orig })

	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { tryVersion(log) })
	if !exited {
		t.Error("expected tryVersion to call log.Exit when --version flag is true")
	}
}

// ---------------------------------------------------------------------------
// tryCheckShred — loop body (tagSet[t] = true) requires a non-empty BuildTags
// slice, injected via the versionGet seam.
// ---------------------------------------------------------------------------

func TestTryCheckShred_TagLoopBranch(t *testing.T) {
	v := true
	flagCheckShred = &v

	orig := versionGet
	versionGet = func() version.Info { return version.Info{BuildTags: []string{"fips", "no_prom"}} }
	t.Cleanup(func() { versionGet = orig })

	log := logging.New(logging.Config{Out: io.Discard})
	_, exited := trapExit(log, func() { tryCheckShred(log) })
	if !exited {
		t.Error("expected tryCheckShred to call log.Exit when --check-shred flag is true")
	}
}

// ---------------------------------------------------------------------------
// tryValidateConfig — config-invalid fatal branch. The configValidate seam is
// overridden to return an error so log.Fatal (ExitFn(1)) is exercised.
// ---------------------------------------------------------------------------

func TestTryValidateConfig_InvalidConfigCallsFatal(t *testing.T) {
	v := false
	flagValidate = &v

	orig := configValidate
	configValidate = func(_ keelconfig.Config) error { return errors.New("bad config") }
	t.Cleanup(func() { configValidate = orig })

	log := logging.New(logging.Config{Out: io.Discard})
	code, exited := trapExit(log, func() { _ = tryValidateConfig(log) })
	if !exited {
		t.Fatal("expected tryValidateConfig to call log.Fatal on invalid config")
	}
	if code != 1 {
		t.Errorf("expected exit code 1 from log.Fatal, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// TryVersion — exported entry point; parses flags then runs the (no-op) tries
// when no terminal flags are set.
// ---------------------------------------------------------------------------

func TestTryVersion_ParsesAndReturns(t *testing.T) {
	fv, fi, fs := false, false, false
	flagVersion = &fv
	flagCheckIntegrity = &fi
	flagCheckShred = &fs

	log := logging.New(logging.Config{Out: io.Discard})
	TryVersion(log)
}

// ---------------------------------------------------------------------------
// ProcessArgs — exported entry point; with no terminal flags set it returns
// the default config.
// ---------------------------------------------------------------------------

func TestProcessArgs_ReturnsDefaultConfig(t *testing.T) {
	fv, fi, fs, fval := false, false, false, false
	flagVersion = &fv
	flagCheckIntegrity = &fi
	flagCheckShred = &fs
	flagValidate = &fval

	log := logging.New(logging.Config{Out: io.Discard})
	cfg := ProcessArgs(log)
	if cfg.Listeners.HTTP.Port == 0 {
		t.Error("expected non-zero HTTP port from ProcessArgs default config")
	}
}
