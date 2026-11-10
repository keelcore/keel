package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	core "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
)

func TestAdminReload_WrongMethod_Returns405(t *testing.T) {
	s := core.NewServer(logging.New(logging.Config{}), config.Config{})
	rr := httptest.NewRecorder()
	s.ReloadHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/reload", nil))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}
