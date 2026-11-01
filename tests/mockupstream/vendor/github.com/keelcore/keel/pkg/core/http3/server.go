//go:build !no_h3

package http3

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
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

// Limits carries the request/connection limits the TCP listeners enforce, so
// HTTP/3 applies the same policy instead of quic-go's looser defaults.
type Limits struct {
	MaxHeaderBytes int
	IdleTimeout    time.Duration
}

// NewServer creates a Server for a pre-bound UDP socket (see Serve). tlsCfg is
// keel's TLS policy (TLS 1.3, FIPS curves) with GetCertificate set, so cert
// rotation reaches H3. 0-RTT stays disabled: quic-go enables it when QUICConfig
// is nil, and 0-RTT requests are replayable.
func NewServer(h http.Handler, tlsCfg *tls.Config, lim Limits) *Server {
	return &Server{
		srv: &qhttp3.Server{
			Handler:        h,
			TLSConfig:      tlsCfg,
			MaxHeaderBytes: lim.MaxHeaderBytes,
			IdleTimeout:    lim.IdleTimeout,
			QUICConfig: &quic.Config{
				Allow0RTT:      false,
				MaxIdleTimeout: lim.IdleTimeout,
			},
		},
	}
}

// Serve runs the server on an already-bound UDP socket. Binding before serving
// lets the caller report readiness only once the port is actually held. The
// caller owns conn: Shutdown does not close it.
func (s *Server) Serve(conn net.PacketConn) error {
	ps, ok := s.srv.(interface{ Serve(net.PacketConn) error })
	if !ok {
		return errors.New("http3 backend cannot serve a pre-bound socket")
	}
	return ps.Serve(conn)
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
