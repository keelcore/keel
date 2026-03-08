// examples/myapp/handler.go
// Application route handlers.
// Keel's middleware chain (TLS, OWASP, authn, access log) wraps all routes
// registered on the HTTPS port automatically.
package main

import (
	"fmt"
	"net/http"
)

func hello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "hello, from downstream app based on keel library")
}