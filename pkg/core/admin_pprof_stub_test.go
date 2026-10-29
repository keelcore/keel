//go:build no_pprof

package core

import (
	"net/http"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// no_pprof: admin.pprof logs pprof_not_compiled and /debug/pprof/ stays 404.
func TestAdminMux_PProfNotCompiled(t *testing.T) {
	var buf syncBuffer
	var cfg config.Config
	cfg.Admin = config.AdminConfig{Token: "adm", PProf: true}
	s := NewServer(logging.New(logging.Config{JSON: true, Out: &buf}), cfg, WithConfigPaths("", ""))
	if code := adminStatus(t, s.adminMux(), http.MethodGet, "/debug/pprof/", "adm"); code != http.StatusNotFound {
		t.Errorf("pprof in no_pprof build: %d, want 404", code)
	}
	if !strings.Contains(buf.String(), "pprof_not_compiled") {
		t.Errorf("missing pprof_not_compiled warning: %s", buf.String())
	}
}
