//go:build no_authn

package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

func AuthnJWT(_ config.Config, next http.Handler, _ *logging.Logger) http.Handler {
	return next
}
