//go:build !no_prom

package core

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/metrics"
	"github.com/keelcore/keel/pkg/core/router"
)

// runCertExpiryLoop: exits immediately when ctx is already cancelled.
// A non-existent cert file causes CertExpirySeconds to error (skipped silently),
// then the ticker select immediately resolves ctx.Done.
func TestRunCertExpiryLoop_ExitsOnCancel(t *testing.T) {
	met := metrics.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runCertExpiryLoop(ctx, "/nonexistent-cert-for-test.pem", met)
}

// runLogDropsLoop: exits immediately when ctx is already cancelled.
func TestRunLogDropsLoop_ExitsOnCancel(t *testing.T) {
	met := metrics.New()
	sink := logging.NewHTTPSink("http://127.0.0.1:1", 10, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runLogDropsLoop(ctx, sink, met)
}

// AddRoute: appends a registrar and the closure body registers the handler on the router.
func TestServer_AddRoute(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	s := NewServer(log, config.Config{})

	before := len(s.registrars)
	s.AddRoute(8080, "/probe", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if len(s.registrars) != before+1 {
		t.Errorf("expected registrars len %d after AddRoute, got %d", before+1, len(s.registrars))
	}

	// Exercise the registrar closure body: call Register on a real router and verify the route exists.
	rt := router.New()
	for _, reg := range s.registrars[before:] {
		reg.Register(rt)
	}
	if !rt.Has(8080, "/probe") {
		t.Errorf("expected route /probe on port 8080 after Register, not found")
	}
}