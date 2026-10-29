package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

func adminStatus(t *testing.T, h http.Handler, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRequireBearer(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if code := adminStatus(t, requireBearer("", ok), http.MethodGet, "/", "x"); code != http.StatusForbidden {
		t.Errorf("unset token: %d, want 403", code)
	}
	h := requireBearer("s3cret", ok)
	if code := adminStatus(t, h, http.MethodGet, "/", ""); code != http.StatusUnauthorized {
		t.Errorf("no header: %d, want 401", code)
	}
	if code := adminStatus(t, h, http.MethodGet, "/", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d, want 401", code)
	}
	if code := adminStatus(t, h, http.MethodGet, "/", "s3cret"); code != http.StatusOK {
		t.Errorf("right token: %d, want 200", code)
	}
}

func adminServer(admin config.AdminConfig) *Server {
	var cfg config.Config
	cfg.Admin = admin
	cfg.Metrics.Prometheus = true
	return NewServer(logging.New(logging.Config{Out: io.Discard}), cfg, WithConfigPaths("", ""))
}

// Probes and /version stay open; reload needs the admin token; pprof is absent
// unless enabled; /metrics is open unless a metrics token is configured.
func TestAdminMux_PerEndpointAccess(t *testing.T) {
	mux := adminServer(config.AdminConfig{}).adminMux()
	for _, p := range []string{"/healthz", "/version"} {
		if code := adminStatus(t, mux, http.MethodGet, p, ""); code != http.StatusOK {
			t.Errorf("%s: %d, want 200 (open)", p, code)
		}
	}
	if code := adminStatus(t, mux, http.MethodPost, "/admin/reload", ""); code != http.StatusForbidden {
		t.Errorf("reload without admin.token configured: %d, want 403", code)
	}
	if code := adminStatus(t, mux, http.MethodGet, "/debug/pprof/", ""); code != http.StatusNotFound {
		t.Errorf("pprof disabled: %d, want 404", code)
	}
	if code := adminStatus(t, mux, http.MethodGet, "/metrics", ""); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Errorf("metrics without metrics_token: %d, want open", code)
	}

	mux = adminServer(config.AdminConfig{Token: "adm", MetricsToken: "prom", PProf: true}).adminMux()
	if code := adminStatus(t, mux, http.MethodPost, "/admin/reload", ""); code != http.StatusUnauthorized {
		t.Errorf("reload without bearer: %d, want 401", code)
	}
	if code := adminStatus(t, mux, http.MethodPost, "/admin/reload", "adm"); code != http.StatusOK {
		t.Errorf("reload with admin token: %d, want 200", code)
	}
	if code := adminStatus(t, mux, http.MethodGet, "/debug/pprof/", ""); code != http.StatusUnauthorized {
		t.Errorf("pprof without bearer: %d, want 401", code)
	}
	if code := adminStatus(t, mux, http.MethodGet, "/debug/pprof/", "adm"); code != http.StatusOK {
		t.Errorf("pprof with admin token: %d, want 200", code)
	}
	if code := adminStatus(t, mux, http.MethodGet, "/metrics", ""); code != http.StatusUnauthorized {
		t.Errorf("metrics without bearer when metrics_token set: %d, want 401", code)
	}
	if code := adminStatus(t, mux, http.MethodGet, "/metrics", "prom"); code != http.StatusOK {
		t.Errorf("metrics with metrics token: %d, want 200", code)
	}
}

// Admin tokens never appear in the SIGUSR1 dump; an unset one stays empty.
func TestRedactedConfig_AdminTokens(t *testing.T) {
	var cfg config.Config
	cfg.Admin.Token = "adm-secret-token"
	b, _ := json.Marshal(redactedConfig(cfg))
	if strings.Contains(string(b), "adm-secret-token") {
		t.Fatalf("admin token leaked: %s", b)
	}
	if redactSecret("") != "" {
		t.Error("unset secret should stay empty")
	}
}
