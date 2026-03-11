//go:build no_authn

package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

func AuthnJWT(_ config.Config, _ func() []string, next http.Handler, _ *logging.Logger) http.Handler {
	return next
}

func LoadTrustedSigners(_ config.AuthnConfig, _ *logging.Logger) []string { return nil }
