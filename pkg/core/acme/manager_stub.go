//go:build no_acme

package acme

import (
	"context"
	"errors"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

// Manager is a no-op stub used when the no_acme build tag is active.
type Manager struct{}

func New() *Manager                                                   { return &Manager{} }
func (m *Manager) SetToken(_, _ string)                               {}
func (m *Manager) DeleteToken(_ string)                               {}
func (m *Manager) HTTPHandler(_ int) http.Handler                     { return http.NotFoundHandler() }
func (m *Manager) Start(_ context.Context, _ config.ACMEConfig) error { return nil }

// Validate returns an error if ACME is configured but not compiled in.
func Validate(cfg config.Config) error {
	if cfg.TLS.ACME.Enabled {
		return errors.New("ACME support not built (binary compiled with no_acme tag)")
	}
	return nil
}
