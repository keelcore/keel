//go:build no_pprof

package core

import "net/http"

// mountPProf: profiling is compiled out (no_pprof), so admin.pprof has no
// effect; say so instead of silently serving 404.
func (s *Server) mountPProf(_ *http.ServeMux, _ string) {
	s.logger.Warn("pprof_not_compiled", map[string]any{"hint": "admin.pprof is ignored in no_pprof builds"})
}
