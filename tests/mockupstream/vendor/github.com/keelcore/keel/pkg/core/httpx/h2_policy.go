//go:build !no_h2

package httpx

import "net/http"

// ApplyHTTP2Policy is a seam for enabling/disabling HTTP/2.
// Default: do nothing (Go enables HTTP/2 over TLS when possible).
func ApplyHTTP2Policy(_ *http.Server) {}

// NextProtos is the ALPN list for keel's TLS listeners: HTTP/2 preferred.
func NextProtos() []string { return []string{"h2", "http/1.1"} }
