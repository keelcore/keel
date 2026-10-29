//go:build !no_sidecar

package sidecar

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

// Both branches get the default dial/handshake/idle timeouts, no idle or
// per-host connection cap, no environment proxy, HTTP/2 attempts and the
// response-header deadline.
func TestUpstreamTransport_PoolAndTimeouts(t *testing.T) {
	for _, tlsOn := range []bool{false, true} {
		sc := config.SidecarConfig{UpstreamURL: "http://127.0.0.1:1", UpstreamTLS: config.UpstreamTLSConfig{Enabled: tlsOn}}
		tr, err := upstreamTransport(sc, 7*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if tr.MaxIdleConnsPerHost != unlimitedIdle || tr.MaxIdleConns != 0 || tr.MaxConnsPerHost != 0 {
			t.Errorf("tls=%v: pool caps idle/host=%d idle=%d conns/host=%d", tlsOn, tr.MaxIdleConnsPerHost, tr.MaxIdleConns, tr.MaxConnsPerHost)
		}
		if tr.Proxy != nil || !tr.ForceAttemptHTTP2 || tr.ResponseHeaderTimeout != 7*time.Second {
			t.Errorf("tls=%v: proxy=%v h2=%v rht=%v", tlsOn, tr.Proxy != nil, tr.ForceAttemptHTTP2, tr.ResponseHeaderTimeout)
		}
		if tr.TLSHandshakeTimeout == 0 || tr.IdleConnTimeout == 0 {
			t.Errorf("tls=%v: missing default timeouts (handshake=%v idle=%v)", tlsOn, tr.TLSHandshakeTimeout, tr.IdleConnTimeout)
		}
	}
	if _, err := upstreamTransport(config.SidecarConfig{UpstreamTLS: config.UpstreamTLSConfig{Enabled: true, CAFile: "/nonexistent"}}, 0); err == nil {
		t.Error("bad CA file: expected error")
	}
}

func proxyGet(t *testing.T, cfg config.Config) *httptest.ResponseRecorder {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil))
	return rec
}

// upstream_h2c: requests reach the upstream as plaintext HTTP/2.
func TestNew_UpstreamH2C(t *testing.T) {
	var proto string
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { proto = r.Proto }))
	up.Config.Protocols = new(http.Protocols)
	up.Config.Protocols.SetUnencryptedHTTP2(true)
	up.Start()
	defer up.Close()

	var cfg config.Config
	cfg.Sidecar.UpstreamURL = up.URL
	cfg.Sidecar.UpstreamH2C = true
	if rec := proxyGet(t, cfg); rec.Code != http.StatusOK || proto != "HTTP/2.0" {
		t.Fatalf("status %d proto %q, want 200 HTTP/2.0", rec.Code, proto)
	}
}

// A unix:// upstream_url is dialed over the socket and addressed as localhost.
func TestNew_UnixSocketUpstream(t *testing.T) {
	dir, err := os.MkdirTemp("", "ks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "app.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var host string
	srv := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { host = r.Host })}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	var cfg config.Config
	cfg.Sidecar.UpstreamURL = "unix://" + sock
	if rec := proxyGet(t, cfg); rec.Code != http.StatusOK || host != "localhost" {
		t.Fatalf("status %d host %q, want 200 localhost", rec.Code, host)
	}
	if upstreamBase("http://app:9000") != "http://app:9000" {
		t.Error("http upstream_url altered")
	}
}

// A hung upstream frees the connection at timeouts.write: 502, not a hang.
func TestNew_HungUpstreamTimesOut(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer up.Close()
	defer close(release)

	var cfg config.Config
	cfg.Sidecar.UpstreamURL = up.URL
	cfg.Timeouts.Write = config.Duration{Duration: 200 * time.Millisecond}
	start := time.Now()
	if rec := proxyGet(t, cfg); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("hung upstream held the request %v", d)
	}
}
