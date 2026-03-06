//go:build no_h2

package httpx

import (
	"crypto/tls"
	"net/http"
)

// ApplyHTTP2Policy disables HTTP/2 by overriding TLSNextProto with an empty map.
func ApplyHTTP2Policy(s *http.Server) {
	s.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
}
