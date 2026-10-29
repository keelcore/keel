//go:build !no_sidecar

package sidecar

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// StartHealthProbe starts a background goroutine that periodically GETs
// cfg.UpstreamURL+cfg.UpstreamHealthPath and reports it as the "upstream"
// readiness check (aggregated by /readyz; separate from backpressure).
// If transport is nil, one is built from cfg.UpstreamTLS (as the proxy uses).
// If log is nil, probe failures are silently swallowed.
func StartHealthProbe(
	ctx context.Context,
	cfg config.SidecarConfig,
	transport http.RoundTripper,
	readiness *probes.Readiness,
	log *logging.Logger,
) {
	// The upstream has its own readiness flag, reported as the "upstream"
	// check: /readyz aggregates it with keel's other signals (a sidecar cannot
	// be ready while its upstream is not), and it never touches the
	// backpressure flag the shedding middleware reads. Not ready until the
	// first successful probe.
	h := &upstreamHealth{}
	readiness.AddCheck("upstream", h.check)

	if transport == nil {
		// Probe over the same upstream transport as the proxy (TLS/mTLS, h2c,
		// unix socket); a bare default transport would miss them and never ready.
		t, err := upstreamTransport(cfg, 0)
		if err != nil {
			if log != nil {
				log.Warn("upstream_health_transport_failed", map[string]any{"err": err.Error()})
			}
			return
		}
		transport = t
	}
	timeout := cfg.UpstreamHealthTimeout.Duration
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	interval := cfg.UpstreamHealthInterval.Duration
	if interval == 0 {
		interval = 10 * time.Second
	}

	client := &http.Client{Transport: transport, Timeout: timeout}
	healthURL := strings.TrimRight(upstreamBase(cfg.UpstreamURL), "/") + cfg.UpstreamHealthPath

	go func() {
		doProbe(client, healthURL, h, log)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				doProbe(client, healthURL, h, log)
			}
		}
	}()
}

// upstreamHealth is the sidecar's own readiness signal for its upstream.
type upstreamHealth struct{ ok atomic.Bool }

func (h *upstreamHealth) check() error {
	if !h.ok.Load() {
		return errors.New("upstream not ready")
	}
	return nil
}

func doProbe(client *http.Client, url string, h *upstreamHealth, log *logging.Logger) {
	resp, err := client.Get(url) //nolint:noctx // client timeout controls the deadline
	if err != nil {
		h.ok.Store(false)
		if log != nil {
			log.Warn("upstream_health_probe_failed", map[string]any{"err": err.Error()})
		}
		return
	}
	resp.Body.Close()
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300
	h.ok.Store(healthy)
	if !healthy && log != nil {
		log.Warn("upstream_unhealthy", map[string]any{"status": resp.StatusCode})
	}
}
