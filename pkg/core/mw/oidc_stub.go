//go:build no_oidc || no_authn

package mw

import (
	"errors"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
)

// OIDC is compiled out by the no_oidc or no_authn build tags.
func OIDC(config.OIDCConfig, http.Handler, http.Handler, *logging.Logger, func(audit.Event)) (http.Handler, error) {
	return nil, errors.New("oidc compiled out by the no_oidc or no_authn build tag")
}
