//go:build !no_h3

package core

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// TestServeH3_CancelledContext: valid cert files, pre-cancelled context.
// The ctx.Done() branch in the select is taken before ListenAndServeTLS can
// error, so serveH3 returns nil.
// Guarded !no_h3: the stub returns an error immediately, making the select
// race non-deterministic on no_h3 builds.
func TestServeH3_CancelledContext(t *testing.T) {
	certFile, keyFile := generateTestCert(t)

	cfg := shortDrainCfg()
	cfg.TLS.CertFile = certFile
	cfg.TLS.KeyFile = keyFile

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := serveH3(ctx, "127.0.0.1:0", http.NotFoundHandler(), cfg,
		logging.New(logging.Config{Out: io.Discard})); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}