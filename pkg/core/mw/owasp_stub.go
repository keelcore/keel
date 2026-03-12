//go:build no_owasp

package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/config"
)

// OWASP is a no-op stub used when the no_owasp build tag is active.
// All security headers, body limits, and per-request timeouts are skipped.
func OWASP(_ config.Config, next http.Handler) http.Handler { return next }