//go:build !no_h3

package http3

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	qhttp3 "github.com/quic-go/quic-go/http3"
)

// mockBackend is a Backend the test fully controls; it never binds a port.
type mockBackend struct {
	listenErr   error
	shutdownErr error
	listenCert  string
	listenKey   string
	shutdownCtx context.Context
}

func (m *mockBackend) ListenAndServeTLS(certFile, keyFile string) error {
	m.listenCert = certFile
	m.listenKey = keyFile
	return m.listenErr
}

func (m *mockBackend) Shutdown(ctx context.Context) error {
	m.shutdownCtx = ctx
	return m.shutdownErr
}

func TestNew(t *testing.T) {
	// New constructs a real quic-go backend but must not bind anything.
	s := New("127.0.0.1:0", http.NotFoundHandler(), &tls.Config{})
	if s == nil || s.srv == nil {
		t.Fatal("New returned incomplete Server")
	}
	qs, ok := s.srv.(*qhttp3.Server)
	if !ok {
		t.Fatalf("backend type = %T, want *qhttp3.Server", s.srv)
	}
	if qs.Addr != "127.0.0.1:0" {
		t.Fatalf("Addr = %q", qs.Addr)
	}
}

func TestListenAndServeTLSSuccess(t *testing.T) {
	mb := &mockBackend{}
	s := NewWithBackend(mb)
	if err := s.ListenAndServeTLS("cert.pem", "key.pem"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mb.listenCert != "cert.pem" || mb.listenKey != "key.pem" {
		t.Fatalf("args not forwarded: %q %q", mb.listenCert, mb.listenKey)
	}
}

func TestListenAndServeTLSError(t *testing.T) {
	want := errors.New("listen boom")
	s := NewWithBackend(&mockBackend{listenErr: want})
	if err := s.ListenAndServeTLS("c", "k"); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func TestShutdownSuccess(t *testing.T) {
	mb := &mockBackend{}
	s := NewWithBackend(mb)
	ctx := context.Background()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mb.shutdownCtx != ctx {
		t.Fatal("context not forwarded")
	}
}

func TestShutdownError(t *testing.T) {
	want := errors.New("shutdown boom")
	s := NewWithBackend(&mockBackend{shutdownErr: want})
	err := s.Shutdown(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("want wrapped %v, got %v", want, err)
	}
	if got := err.Error(); got != "h3 shutdown: shutdown boom" {
		t.Fatalf("wrap format = %q", got)
	}
}

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

// New: creates a non-nil Server wrapping a real quic-go HTTP/3 server.
func TestHTTP3_New_CreatesServer(t *testing.T) {
	s := New(":0", http.DefaultServeMux, &tls.Config{})
	if s == nil {
		t.Error("expected non-nil *http3.Server from New")
	}
}

// ListenAndServeTLS: bad cert path → error before any network bind.
func TestHTTP3_ListenAndServeTLS_BadCert(t *testing.T) {
	s := New(":0", http.DefaultServeMux, &tls.Config{})
	err := s.ListenAndServeTLS("/no/such/cert.pem", "/no/such/key.pem")
	if err == nil {
		t.Error("expected error from ListenAndServeTLS with missing cert files")
	}
}

// Shutdown: calling Shutdown on a never-started server must not panic.
func TestHTTP3_Shutdown_NotStarted(t *testing.T) {
	s := New(":0", http.DefaultServeMux, &tls.Config{})
	_ = s.Shutdown(context.Background())
}
