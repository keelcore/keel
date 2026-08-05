//go:build !no_h3

package core

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// realRunner.serveH3 delegates to serveH3. A pre-cancelled context takes the
// ctx.Done() branch and returns nil. Guarded !no_h3 so the underlying serveH3
// is the real (deterministic) implementation, not the erroring stub.
func TestRealRunner_ServeH3(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := shortDrainCfg()
	cfg.TLS.CertFile = certFile
	cfg.TLS.KeyFile = keyFile

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := (realRunner{}).serveH3(ctx, "127.0.0.1:0", http.NotFoundHandler(), cfg,
		logging.New(logging.Config{Out: io.Discard})); err != nil {
		t.Fatalf("realRunner.serveH3: %v", err)
	}
}
