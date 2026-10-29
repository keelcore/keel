//go:build !no_h3

package core

import (
	"context"
	cryptotls "crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	qhttp3 "github.com/quic-go/quic-go/http3"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

func skipUnderFIPS(t *testing.T) {
	t.Helper()
	if os.Getenv("GOFIPS140") != "" || strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
		t.Skip("self-signed cert + InsecureSkipVerify not permitted under FIPS 140-only mode")
	}
}

func discardLog() *logging.Logger { return logging.New(logging.Config{Out: io.Discard}) }

// End to end: serveH3With binds, serves a real HTTP/3 request using the shared
// cert source, then shuts down cleanly on cancel (drain from cfg).
func TestServeH3With_ServesRequestAndShutsDown(t *testing.T) {
	skipUnderFIPS(t)
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.LocalAddr().String()
	_ = probe.Close()

	cfg := shortDrainCfg()
	ctx, cancel := context.WithCancel(context.Background())
	bound := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveH3With(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "h3-ok")
		}), cfg, discardLog(), func() { close(bound) }, loader.Get)
	}()
	<-bound

	tr := &qhttp3.Transport{TLSClientConfig: &cryptotls.Config{InsecureSkipVerify: true}} //nolint:gosec // self-signed test cert
	defer tr.Close()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		cancel()
		t.Fatalf("H3 GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "h3-ok" || resp.ProtoMajor != 3 {
		t.Fatalf("got proto %d body %q", resp.ProtoMajor, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveH3With returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveH3With did not return after cancel")
	}
}

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

// A bind failure is returned and onBound never fires (no false readiness).
func TestServeH3With_ListenError(t *testing.T) {
	orig := h3ListenPacket
	h3ListenPacket = func(string, string) (net.PacketConn, error) { return nil, errors.New("address already in use") }
	t.Cleanup(func() { h3ListenPacket = orig })
	fired := false
	err := serveH3With(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), config.Config{}, discardLog(), func() { fired = true }, nil)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("err = %v, want listen error", err)
	}
	if fired {
		t.Fatal("onBound fired although the socket was never bound")
	}
}

// If serving fails (socket closed underneath), serveH3With returns promptly.
func TestServeH3With_ServeErrorReturns(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := config.Config{}
	cfg.TLS.CertFile, cfg.TLS.KeyFile = certFile, keyFile
	var bound net.PacketConn
	orig := h3ListenPacket
	h3ListenPacket = func(n, a string) (net.PacketConn, error) {
		c, err := orig(n, a)
		bound = c
		return c, err
	}
	t.Cleanup(func() { h3ListenPacket = orig })
	done := make(chan error, 1)
	go func() {
		done <- serveH3With(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), cfg, discardLog(),
			func() { go func() { time.Sleep(100 * time.Millisecond); _ = bound.Close() }() }, nil)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serveH3With did not return after its socket failed")
	}
}

func TestH3Certificate(t *testing.T) {
	s := NewServer(discardLog(), config.Config{})
	if _, err := s.h3Certificate(&cryptotls.ClientHelloInfo{}); err == nil {
		t.Fatal("expected error with no cert loader")
	}
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	s.certLoader = loader
	if c, err := s.h3Certificate(&cryptotls.ClientHelloInfo{}); err != nil || c == nil {
		t.Fatalf("got (%v, %v), want the loader's certificate", c, err)
	}
}

// H3 reuses an existing (HTTPS) CertLoader rather than creating its own, so a
// SIGHUP reload of that loader reaches H3.
func TestStartMainListeners_H3ReusesSharedLoader(t *testing.T) {
	certPath, keyPath := writeTempCertKeyPair(t)
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = false
	cfg.Listeners.HTTPS.Enabled = false
	cfg.Listeners.H3.Enabled = true
	cfg.TLS.CertFile, cfg.TLS.KeyFile = certPath, keyPath
	s := newStubServer(t, cfg)
	shared, err := keeltls.NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	s.certLoader = shared
	var wg sync.WaitGroup
	s.startMainListeners(context.Background(), newStubSD(s), &wg, make(chan error, 4), http.NotFoundHandler(), nil)
	wg.Wait()
	if s.certLoader != shared {
		t.Fatal("H3 replaced the shared CertLoader")
	}
}

// H3 with unreadable cert files fails fast at startup.
func TestStartMainListeners_H3CertLoadFatal(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listeners.HTTP.Enabled = false
	cfg.Listeners.HTTPS.Enabled = false
	cfg.Listeners.H3.Enabled = true
	cfg.TLS.CertFile, cfg.TLS.KeyFile = "/nonexistent/h3.crt", "/nonexistent/h3.key"
	s := NewServer(fatalLogger(), cfg)
	s.runner = stubRunner{}
	expectFatal(t, func() {
		s.startMainListeners(context.Background(), newStubSD(s), &sync.WaitGroup{}, make(chan error, 4), http.NotFoundHandler(), nil)
	})
}

// An unreadable client CA fails the H3 listener.
func TestServeH3With_BadClientCAFails(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := config.Config{}
	cfg.TLS.CertFile, cfg.TLS.KeyFile = certFile, keyFile
	cfg.TLS.ClientCAFile = certFile + ".missing"
	err := serveH3With(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), cfg, discardLog(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "client_ca_file") {
		t.Fatalf("err = %v, want client_ca_file error", err)
	}
}
