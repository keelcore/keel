//go:build no_pprof

package probes

import "net/http"

// PProfCompiled reports whether the profiling handlers are in this build.
// no_pprof drops net/http/pprof (and runtime/pprof) from the binary.
const PProfCompiled = false

// RegisterPProf registers nothing in a no_pprof build.
func RegisterPProf(_ *http.ServeMux) {}
