//go:build !no_authn && !no_authz

// server_wrapMain_test.go — wrapMain branches requiring both authn and authz.
package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// wrapMain with authn enabled wraps the handler (missing bearer → 401).
func TestWrapMain_AuthnEnabled(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Security:     config.SecurityConfig{OWASPHeaders: false},
		Backpressure: config.BackpressureConfig{SheddingEnabled: false},
		Authn: config.AuthnConfig{
			Enabled: true,
			// No trusted signers → any token fails; no bearer → 401.
		},
		ExtAuthz: config.ExtAuthzConfig{Enabled: false},
		Logging:  config.LoggingConfig{AccessLog: false},
		Limits:   config.LimitsConfig{MaxConcurrent: 0},
	}
	s := NewServer(log, cfg)
	s.applyAuthnState(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.wrapMain(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header → AuthnJWT returns 401.
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when authn enabled with no bearer token, got %d", rr.Code)
	}
}
