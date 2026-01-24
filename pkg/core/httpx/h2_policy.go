//go:build !no_h2

package httpx

import "net/http"

// ApplyHTTP2Policy is a seam for enabling/disabling HTTP/2.
// Default: do nothing (Go enables HTTP/2 over TLS when possible).
func ApplyHTTP2Policy(_ *http.Server) {}
