//go:build !no_h3

package core

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

// With no shutdown_drain configured the 10s default bounds the drain.
func TestServeH3With_DefaultDrain(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := config.Config{}
	cfg.TLS.CertFile, cfg.TLS.KeyFile = certFile, keyFile
	ctx, cancel := context.WithCancel(context.Background())
	bound := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveH3With(ctx, "127.0.0.1:0", http.NotFoundHandler(), cfg, discardLog(), func() { close(bound) }, nil)
	}()
	<-bound
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveH3With did not return after cancel")
	}
}
