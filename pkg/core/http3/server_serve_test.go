//go:build !no_h3

package http3

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"

	qhttp3 "github.com/quic-go/quic-go/http3"
)

// NewServer carries keel's limits into quic-go and keeps 0-RTT off (quic-go
// enables 0-RTT when QUICConfig is nil, and 0-RTT requests are replayable).
func TestNewServer_AppliesLimitsAndDisables0RTT(t *testing.T) {
	s := NewServer(http.NotFoundHandler(), &tls.Config{}, Limits{MaxHeaderBytes: 65536, IdleTimeout: 7 * time.Second})
	qs, ok := s.srv.(*qhttp3.Server)
	if !ok {
		t.Fatalf("backend type = %T, want *qhttp3.Server", s.srv)
	}
	if qs.MaxHeaderBytes != 65536 {
		t.Errorf("MaxHeaderBytes = %d, want 65536", qs.MaxHeaderBytes)
	}
	if qs.IdleTimeout != 7*time.Second {
		t.Errorf("IdleTimeout = %v, want 7s", qs.IdleTimeout)
	}
	if qs.QUICConfig == nil || qs.QUICConfig.Allow0RTT {
		t.Errorf("QUICConfig must be set with Allow0RTT=false, got %+v", qs.QUICConfig)
	}
	if qs.QUICConfig.MaxIdleTimeout != 7*time.Second {
		t.Errorf("QUIC MaxIdleTimeout = %v, want 7s", qs.QUICConfig.MaxIdleTimeout)
	}
}

// A backend that cannot serve a pre-bound socket is rejected, not panicked on.
func TestServe_BackendWithoutServe(t *testing.T) {
	if err := NewWithBackend(&mockBackend{}).Serve(nil); err == nil {
		t.Fatal("expected error for a backend without Serve")
	}
}

// The real backend serves the caller's socket and returns once it is closed.
func TestServe_RealBackendReturnsOnClosedConn(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	s := NewServer(http.NotFoundHandler(), &tls.Config{}, Limits{})
	done := make(chan error, 1)
	go func() { done <- s.Serve(conn) }()
	time.Sleep(100 * time.Millisecond)
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its socket was closed")
	}
}
