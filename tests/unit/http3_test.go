//go:build !no_h3

// tests/unit/http3_gaps_test.go
package unit

import (
	"context"
	"errors"
	"strings"
	"testing"

	khttp3 "github.com/keelcore/keel/pkg/core/http3"
)

// ---------------------------------------------------------------------------
// Mock-backend tests — cover error branch of Shutdown (unreachable via
// a real quic-go server that was never started).
// ---------------------------------------------------------------------------

// mockH3Backend implements khttp3.Backend; Shutdown always returns an error.
type mockH3Backend struct{}

func (mockH3Backend) ListenAndServeTLS(_, _ string) error { return nil }
func (mockH3Backend) Shutdown(_ context.Context) error    { return errors.New("mock shutdown error") }

// NewWithBackend + Shutdown error path: wraps the error with "h3 shutdown:".
func TestHTTP3_Shutdown_ErrorWrapped(t *testing.T) {
	s := khttp3.NewWithBackend(mockH3Backend{})
	err := s.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected error from Shutdown, got nil")
	}
	if !strings.Contains(err.Error(), "h3 shutdown") {
		t.Errorf("expected 'h3 shutdown' prefix in error, got %v", err)
	}
}

// NewWithBackend + ListenAndServeTLS: delegates to Backend.
func TestHTTP3_NewWithBackend_ListenAndServeTLS(t *testing.T) {
	s := khttp3.NewWithBackend(mockH3Backend{})
	err := s.ListenAndServeTLS("cert.pem", "key.pem") // mock returns nil
	if err != nil {
		t.Errorf("expected nil from mock ListenAndServeTLS, got %v", err)
	}
}
