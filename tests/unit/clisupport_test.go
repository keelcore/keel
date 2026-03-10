package unit

import (
	"context"
	"io"
	"testing"

	"github.com/keelcore/keel/pkg/clisupport"
	"github.com/keelcore/keel/pkg/config"
	core "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/ports"
)

// TryVersion: with --version flag absent, returns without calling os.Exit.
func TestTryVersion_NoFlag(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	clisupport.TryVersion(log)
}

// TryValidateApp: with --validate flag absent, returns without calling os.Exit.
func TestTryValidateApp_NoFlag(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	clisupport.TryValidateApp(log)
}

// ProcessArgs: loads defaults when KEEL_CONFIG/KEEL_SECRETS are unset,
// validates successfully, and returns the config (--validate flag not set).
func TestProcessArgs_DefaultEnv(t *testing.T) {
	t.Setenv("KEEL_CONFIG", "")
	t.Setenv("KEEL_SECRETS", "")
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := clisupport.ProcessArgs(log)
	if cfg.Listeners.HTTP.Port != ports.HTTP {
		t.Errorf("expected HTTP port %d, got %d", ports.HTTP, cfg.Listeners.HTTP.Port)
	}
}

// RunServer: with all listeners disabled and a pre-cancelled context, Run
// exits immediately via the ctx.Done branch without binding any ports.
func TestRunServer_CancelledContext(t *testing.T) {
	cfg := config.Config{
		Listeners: config.ListenersConfig{
			HTTP:    config.ListenerConfig{Enabled: false},
			HTTPS:   config.ListenerConfig{Enabled: false},
			H3:      config.ListenerConfig{Enabled: false},
			Health:  config.ListenerConfig{Enabled: false},
			Ready:   config.ListenerConfig{Enabled: false},
			Startup: config.ListenerConfig{Enabled: false},
			Admin:   config.ListenerConfig{Enabled: false},
		},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
	}
	log := logging.New(logging.Config{Out: io.Discard})
	s := core.NewServer(log, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: Run exits at WaitForStop without blocking

	core.RunServer(s, ctx)
}