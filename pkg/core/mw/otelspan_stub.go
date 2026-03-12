//go:build no_otel

package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/core/tracing"
)

// OTelSpan is a no-op passthrough when compiled with no_otel.
func OTelSpan(_ func() *tracing.Exporter, next http.Handler) http.Handler {
	return next
}
