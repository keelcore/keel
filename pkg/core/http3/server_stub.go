//go:build no_h3

package http3

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

type Server struct{}

// Limits mirrors the !no_h3 type so callers compile unchanged.
type Limits struct {
	MaxHeaderBytes int
	IdleTimeout    time.Duration
}

func New(_ string, _ http.Handler, _ *tls.Config) *Server { return &Server{} }

func NewServer(_ http.Handler, _ *tls.Config, _ Limits) *Server { return &Server{} }

func (s *Server) ListenAndServeTLS(_, _ string) error {
	return errors.New("http3 disabled by build tag")
}

func (s *Server) Serve(_ net.PacketConn) error {
	return errors.New("http3 disabled by build tag")
}

func (s *Server) Shutdown(_ context.Context) error { return nil }
