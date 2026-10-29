//go:build !no_acme

package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// A failed ACME start is logged, not silently discarded.
func TestStartACMEListener_StartErrorIsLogged(t *testing.T) {
	cfg := config.Defaults()
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"example.test"}
	cfg.TLS.ACME.CACertFile = "/nonexistent/acme-ca.pem" // buildClient fails
	var buf syncBuffer
	s := NewServer(logging.New(logging.Config{JSON: true, Out: &buf}), cfg)
	s.runner = stubRunner{}

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startACMEListener(ctx, newStubSD(s), &wg, errCh)
	wg.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "acme_failed") {
		if time.Now().After(deadline) {
			t.Fatalf("acme_failed not logged: %s", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
