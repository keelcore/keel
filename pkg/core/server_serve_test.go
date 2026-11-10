// server_serve_test.go — white-box tests for serveHTTP, runACMECertExpiryLoop,
// and RunServer (package core).
package core

import (
	"context"
	"testing"

	"github.com/keelcore/keel/pkg/core/acme"
	"github.com/keelcore/keel/pkg/core/metrics"
)

// ---------------------------------------------------------------------------
// serveHTTP — listener bind error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// serveHTTP — clean shutdown on ctx cancel
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// serveHTTPS — listener bind error
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// runACMECertExpiryLoop — exits on ctx cancel
// ---------------------------------------------------------------------------

// runACMECertExpiryLoop exits immediately when ctx is already cancelled.
// Uses a real acme.Manager with no cert stored (CertExpiry errors silently).
func TestRunACMECertExpiryLoop_ExitsOnCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	met := metrics.New()
	mgr := acme.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Should return immediately: ctx is already done.
	runACMECertExpiryLoop(ctx, mgr, met)
}

// ---------------------------------------------------------------------------
// RunServer — compiles and is callable (smoke test)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Run — conditional initialization branches (all listeners disabled)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Run — probe listener goroutine-launch branches (server.go:296-330)
// ---------------------------------------------------------------------------
