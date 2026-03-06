//go:build !no_h3

package http3

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	qhttp3 "github.com/quic-go/quic-go/http3"
)

type Server struct {
	srv *qhttp3.Server
}

func New(addr string, h http.Handler, tlsCfg *tls.Config) *Server {
	return &Server{
		srv: &qhttp3.Server{
			Addr:      addr,
			Handler:   h,
			TLSConfig: tlsCfg,
		},
	}
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
