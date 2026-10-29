//go:build !no_sidecar

package sidecar

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

// StartHealthProbe starts a background goroutine that periodically GETs
// cfg.UpstreamURL+cfg.UpstreamHealthPath and drives readiness accordingly.
// If transport is nil, one is built from cfg.UpstreamTLS (as the proxy uses).
// If log is nil, probe failures are silently swallowed.
func StartHealthProbe(
	ctx context.Context,
	cfg config.SidecarConfig,
	transport http.RoundTripper,
	readiness *probes.Readiness,
	log *logging.Logger,
) {
	if transport == nil {
		// Probe over the same upstream TLS (CA, client cert for mTLS) as the
		// proxy; a bare default transport fails mTLS and the pod never readies.
		t, err := buildTransport(cfg.UpstreamTLS)
		if err != nil {
			readiness.Set(false)
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
	healthURL := strings.TrimRight(cfg.UpstreamURL, "/") + cfg.UpstreamHealthPath

	go func() {
		doProbe(client, healthURL, readiness, log)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				doProbe(client, healthURL, readiness, log)
			}
		}
	}()
}

func doProbe(client *http.Client, url string, readiness *probes.Readiness, log *logging.Logger) {
	resp, err := client.Get(url) //nolint:noctx // client timeout controls the deadline
	if err != nil {
		readiness.Set(false)
		if log != nil {
			log.Warn("upstream_health_probe_failed", map[string]any{"err": err.Error()})
		}
		return
	}
	resp.Body.Close()
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300
	readiness.Set(healthy)
	if !healthy && log != nil {
		log.Warn("upstream_unhealthy", map[string]any{"status": resp.StatusCode})
	}
}
