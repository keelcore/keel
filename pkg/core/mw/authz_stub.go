//go:build no_authz

package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
)

func ExtAuthz(_ config.Config, next http.Handler, _ *logging.Logger, _ ...func(audit.Event)) http.Handler {
	return next
}
