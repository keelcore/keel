//go:build !no_authn && !no_authz

package mw

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/ctxkeys"
	"github.com/keelcore/keel/pkg/core/logging"
)

const auditTestSecret = "0123456789abcdef0123456789abcdef"

func auditAuthn(t *testing.T, trusted []string, next http.Handler) (http.Handler, *[]audit.Event) {
	t.Helper()
	var got []audit.Event
	cfg := config.Defaults()
	cfg.Authn.Enabled = true
	cfg.Authn.TrustedSigners = []string{auditTestSecret}
	cfg.Authn.TrustedIDs = trusted
	h := AuthnJWT(cfg, nil, next, logging.New(logging.Config{Out: io.Discard}),
		WithAudit(func(e audit.Event) { got = append(got, e) }))
	return h, &got
}

func auditRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "203.0.113.7:4321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestAuthnJWT_AuditsEveryOutcome(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, tc := range []struct {
		name    string
		trusted []string
		token   func(t *testing.T) string
		want    audit.Event
	}{
		{"no token", nil, func(*testing.T) string { return "" },
			audit.Event{Event: "authn_failure", SourceIP: "203.0.113.7", Outcome: "deny", Detail: "missing bearer token"}},
		{"bad token", nil, func(*testing.T) string { return "garbage" },
			audit.Event{Event: "authn_failure", SourceIP: "203.0.113.7", Outcome: "deny", Detail: "invalid token"}},
		{"valid", nil, func(t *testing.T) string { return hs256Token(t, auditTestSecret, "alice") },
			audit.Event{Event: "authn_success", Principal: "alice", SourceIP: "203.0.113.7", Outcome: "allow"}},
		{"not on the allow list", []string{"bob"}, func(t *testing.T) string { return hs256Token(t, auditTestSecret, "alice") },
			audit.Event{Event: "authn_forbidden", Principal: "alice", SourceIP: "203.0.113.7", Outcome: "deny"}},
	} {
		h, got := auditAuthn(t, tc.trusted, ok)
		h.ServeHTTP(httptest.NewRecorder(), auditRequest(tc.token(t)))
		if len(*got) != 1 || (*got)[0] != tc.want {
			t.Errorf("%s: events = %+v, want [%+v]", tc.name, *got, tc.want)
		}
	}
}

// The authenticated subject reaches later middleware through the request context.
func TestAuthnJWT_PutsThePrincipalInTheContext(t *testing.T) {
	var principal any
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { principal = r.Context().Value(ctxkeys.Principal) })
	h, _ := auditAuthn(t, nil, next)
	h.ServeHTTP(httptest.NewRecorder(), auditRequest(hs256Token(t, auditTestSecret, "alice")))
	if principal != "alice" {
		t.Fatalf("principal = %v", principal)
	}
}

func TestExtAuthz_AuditsADeny(t *testing.T) {
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"result": false})
	}))
	defer policy.Close()
	cfg := config.Defaults()
	cfg.ExtAuthz.Enabled = true
	cfg.ExtAuthz.Endpoint = policy.URL
	cfg.ExtAuthz.Transport = "opa"
	cfg.ExtAuthz.Timeout = config.DurationOf(time.Second)
	var got []audit.Event
	h := ExtAuthz(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		logging.New(logging.Config{Out: io.Discard}), func(e audit.Event) { got = append(got, e) })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, auditRequest(""))
	want := audit.Event{Event: "authz_deny", SourceIP: "203.0.113.7", Outcome: "deny", Detail: "/x"}
	if rec.Code != http.StatusForbidden || len(got) != 1 || got[0] != want {
		t.Fatalf("status %d, events %+v, want 403 and [%+v]", rec.Code, got, want)
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
