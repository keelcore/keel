// pkg/core/probes/mux.go
package probes

import "net/http"

func RegisterHealth(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

func RegisterReady(mux *http.ServeMux, r *Readiness) {
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if r.Get() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready\n"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
	})
}
