//go:build !no_pprof

package probes

import (
	"net/http"
	"net/http/pprof"
)

// PProfCompiled reports whether the profiling handlers are in this build.
const PProfCompiled = true

// RegisterPProf registers Go runtime profiling handlers under /debug/pprof/
// on mux. These should only be registered on the admin port.
func RegisterPProf(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}
