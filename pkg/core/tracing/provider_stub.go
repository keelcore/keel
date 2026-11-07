//go:build no_otel

// Package tracing provides no-op stubs when compiled with no_otel.
package tracing

import (
	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// Exporter is an opaque stub so that *tracing.Exporter compiles in server.go
// without build-tag ifdefs.
type Exporter struct{}

// Setup is a no-op; OTLP tracing requires a binary built without no_otel.
func Setup(_ config.OTLPConfig) (*Exporter, error) { return nil, nil }

// SetLogger is a no-op.
func (*Exporter) SetLogger(_ *logging.Logger) {}

// Shutdown is a no-op.
func Shutdown(_ *Exporter) {}
