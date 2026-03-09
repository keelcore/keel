// pkg/core/probes/mux.go
package probes

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"strings"
)

// RegisterHealth registers GET /healthz → 200 ok on mux.
func RegisterHealth(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// RegisterReady registers GET /readyz on mux. It evaluates both the atomic
// ready flag and all named checks; on failure the response body lists the
// failing check names so operators can diagnose quickly.
func RegisterReady(mux *http.ServeMux, r *Readiness) {
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		ok, failing := r.IsReady()
		if ok {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready\n"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready: " + strings.Join(failing, ", ") + "\n"))
	})
}

// RegisterFIPS registers GET /health/fips → JSON {"fips_active": bool} on mux.
func RegisterFIPS(mux *http.ServeMux) {
	mux.HandleFunc("/health/fips", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]bool{"fips_active": fipsActive})
	})
}

// RegisterPProf registers Go runtime profiling handlers under /debug/pprof/
// on mux. These should only be registered on the admin port.
func RegisterPProf(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}
