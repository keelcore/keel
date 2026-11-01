//go:build no_owasp

package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

// OWASP (no_owasp build) skips the security headers but keeps Limits: compiling
// the headers out must not lift body caps or the per-request read timeout.
func OWASP(cfg config.Config, next http.Handler) http.Handler { return Limits(cfg, next) }
