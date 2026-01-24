//go:build no_h3

package http3

import (
    "context"
    "crypto/tls"
    "errors"
    "net/http"
)

type Server struct{}

func New(_ string, _ http.Handler, _ *tls.Config) *Server { return &Server{} }

func (s *Server) ListenAndServeTLS(_, _ string) error {
    return errors.New("http3 disabled by build tag")
}

func (s *Server) Shutdown(_ context.Context) error { return nil }
