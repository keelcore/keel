package mw

import (
	"fmt"
	"net/http"
)

// AltSvc advertises HTTP/3 on h3Port (RFC 7838) on every HTTPS response below
// HTTP/3, so h3-capable clients move to it on their next request. maxAge is the
// advertisement's lifetime in seconds (ma).
func AltSvc(h3Port, maxAge int, next http.Handler) http.Handler {
	value := fmt.Sprintf(`h3=":%d"; ma=%d`, h3Port, maxAge)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && r.ProtoMajor < 3 {
			w.Header().Set("Alt-Svc", value)
		}
		next.ServeHTTP(w, r)
	})
}
