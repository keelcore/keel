//go:build !no_h3

package http3

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	qhttp3 "github.com/quic-go/quic-go/http3"
)

// Backend is the minimal interface satisfied by *qhttp3.Server.
// It is exported so that tests can supply a mock via NewWithBackend.
type Backend interface {
	ListenAndServeTLS(certFile, keyFile string) error
	Shutdown(ctx context.Context) error
}

type Server struct {
	srv Backend
}

// New creates a Server backed by a real quic-go HTTP/3 server.
func New(addr string, h http.Handler, tlsCfg *tls.Config) *Server {
	return &Server{
		srv: &qhttp3.Server{
			Addr:      addr,
			Handler:   h,
			TLSConfig: tlsCfg,
		},
	}
}

// NewWithBackend creates a Server using the provided Backend implementation.
// Intended for testing; production code should use New.
func NewWithBackend(b Backend) *Server {
	return &Server{srv: b}
}

func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	return s.srv.ListenAndServeTLS(certFile, keyFile)
}

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("h3 shutdown: %w", err)
	}
	return nil
}
