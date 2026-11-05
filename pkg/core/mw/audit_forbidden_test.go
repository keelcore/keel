package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/audit"
)

// http.ResponseController reaches the wrapped writer, so proxied streams still flush.
func TestAuditForbidden_ExposesTheWrappedWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush = %v", err)
		}
	})
	AuditForbidden(func(audit.Event) {}, next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !rec.Flushed {
		t.Fatal("want the flush to reach the wrapped writer")
	}
}
