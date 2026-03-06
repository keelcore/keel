package mw

import (
	"net/http"

	"github.com/keelcore/keel/pkg/core/probes"
)

func Shedding(r *probes.Readiness, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !r.Get() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("overloaded\n"))
			return
		}
		next.ServeHTTP(w, req)
	})
}
