//go:build !no_otel

package mw

import (
	"net/http/httptest"
	"testing"
)

// otelWriter.Unwrap exposes the wrapped writer.
func TestOtelWriter_Unwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	ow := &otelWriter{ResponseWriter: rr}
	if ow.Unwrap() != rr {
		t.Error("expected Unwrap to return the wrapped ResponseWriter")
	}
}
