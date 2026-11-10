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

// A 403 from the application or upstream is audited too; other statuses are not.
func TestAuditForbidden(t *testing.T) {
	for _, tc := range []struct {
		status int
		events int
	}{{http.StatusForbidden, 1}, {http.StatusOK, 0}, {http.StatusNotFound, 0}} {
		var got []audit.Event
		h := AuditForbidden(func(e audit.Event) { got = append(got, e) },
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
		h.ServeHTTP(httptest.NewRecorder(), auditRequest(""))
		if len(got) != tc.events {
			t.Errorf("status %d: events %+v, want %d", tc.status, got, tc.events)
		}
		if tc.events == 1 && (got[0].Event != "forbidden" || got[0].Detail != "/x") {
			t.Errorf("event = %+v", got[0])
		}
	}
}

func auditRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "203.0.113.7:4321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}
