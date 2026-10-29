//go:build !no_pprof

package core

import (
	"net/http"

	"github.com/keelcore/keel/pkg/core/probes"
)

// mountPProf serves /debug/pprof/ on the admin mux behind the admin token.
func (s *Server) mountPProf(mux *http.ServeMux, token string) {
	pprof := http.NewServeMux()
	probes.RegisterPProf(pprof)
	mux.Handle("/debug/pprof/", requireBearer(token, pprof))
}
