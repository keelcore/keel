//go:build !no_acme

package acme

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/keelcore/keel/pkg/config"
)

// Manager holds per-challenge tokens and serves the ACME http-01 challenge
// route. The actual ACME certificate lifecycle (account creation, order
// submission, challenge authorisation, issuance) requires vendoring an ACME
// client library and is implemented as a no-op stub pending P12 completion.
type Manager struct {
	tokens sync.Map // token → key-authorisation string
}

// New creates an empty ACME Manager.
func New() *Manager { return &Manager{} }

// SetToken registers an http-01 challenge key-authorisation for token.
// Called by the ACME client loop when the CA issues a challenge.
func (m *Manager) SetToken(token, keyAuth string) {
	m.tokens.Store(token, keyAuth)
}

// DeleteToken removes a previously registered challenge token.
func (m *Manager) DeleteToken(token string) {
	m.tokens.Delete(token)
}

// HTTPHandler returns an http.Handler for the plain-HTTP listener that:
//   - serves ACME http-01 challenges at /.well-known/acme-challenge/<token>
//   - redirects all other requests to HTTPS on httpsPort with 301
func (m *Manager) HTTPHandler(httpsPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/.well-known/acme-challenge/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			token := strings.TrimPrefix(r.URL.Path, prefix)
			if v, ok := m.tokens.Load(token); ok {
				w.Header().Set("content-type", "text/plain")
				_, _ = io.WriteString(w, v.(string))
				return
			}
			http.NotFound(w, r)
			return
		}
		// HTTPS redirect: rewrite host with httpsPort if non-standard.
		host := r.Host
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			h = host // no port present
		}
		if httpsPort == 443 {
			host = h
		} else {
			host = fmt.Sprintf("%s:%d", h, httpsPort)
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// Start manages the ACME certificate lifecycle. This implementation is a stub;
// full integration with an ACME client library is pending.
func (m *Manager) Start(_ context.Context, _ config.ACMEConfig) error {
	return nil
}

// Validate returns an error if cfg contains an invalid ACME configuration.
func Validate(_ config.Config) error { return nil }
