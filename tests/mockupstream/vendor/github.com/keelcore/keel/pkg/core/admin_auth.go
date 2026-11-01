package core

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireBearer admits a request only with "Authorization: Bearer <token>",
// compared in constant time. An empty token disables the endpoint (403): a
// privileged admin action must be explicitly configured, never open by default.
func requireBearer(token string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.Error(w, "forbidden: admin token not configured", http.StatusForbidden)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="keel-admin"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// adminMux builds the admin listener's mux with per-endpoint access:
//   - /healthz, /readyz, /startupz, /version, /fips: open (read-only probes)
//   - /metrics: open, or bearer admin.metrics_token when set (Prometheus scrape
//     config: authorization.credentials)
//   - /admin/reload: bearer admin.token (disabled when unset; SIGHUP still works)
//   - /debug/pprof/*: only when admin.pprof is true, and bearer admin.token
func (s *Server) adminMux() *http.ServeMux {
	admin := s.cfg.Admin
	metricsH := s.metricsHandler()
	if admin.MetricsToken != "" {
		metricsH = requireBearer(admin.MetricsToken, metricsH)
	}
	mux := buildAdminMux(s.readiness, s.startup, metricsH, requireBearer(admin.Token, s.ReloadHandler()))
	if admin.PProf {
		s.mountPProf(mux, admin.Token)
	}
	return mux
}
