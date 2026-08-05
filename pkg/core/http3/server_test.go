//go:build !no_h3

package http3

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

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
