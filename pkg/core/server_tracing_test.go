//go:build !no_otel

// server_tracing_test.go — applyTracing success path (requires OTel).
package core

import (
	"io"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// applyTracing with a valid endpoint must store a non-nil exporter.
func TestApplyTracing_ValidEndpoint(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Tracing: config.TracingConfig{
			OTLP: config.OTLPConfig{
				Enabled:  true,
				Endpoint: "localhost:4318",
				Insecure: true,
			},
		},
	}
	s.applyTracing(cfg)
	exp := s.expPtr.Load()
	if exp == nil {
		t.Error("expected non-nil exporter for enabled=true with valid endpoint")
	}
	// Clean up.
	s.applyTracing(config.Config{})
}

// applyTracing second call with OTLP enabled must tear down the first exporter
// and create a new one (tests the Shutdown + re-Setup branch).
func TestApplyTracing_ReloadReplaces(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	cfg := config.Config{
		Tracing: config.TracingConfig{
			OTLP: config.OTLPConfig{Enabled: true, Endpoint: "localhost:4318", Insecure: true},
		},
	}
	s.applyTracing(cfg)
	first := s.expPtr.Load()
	if first == nil {
		t.Fatal("first exporter must be non-nil")
	}
	// Reload with same config: should replace exporter.
	s.applyTracing(cfg)
	second := s.expPtr.Load()
	if second == nil {
		t.Error("second exporter must be non-nil after reload")
	}
	// Clean up.
	s.applyTracing(config.Config{})
}
