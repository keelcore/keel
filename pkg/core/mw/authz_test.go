//go:build !no_authz

package mw

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/audit"
	"github.com/keelcore/keel/pkg/core/logging"
)

// authzHeaders lowercases keys and keeps each field line as its own element.
func TestAuthzHeaders_MultiValue(t *testing.T) {
	h := http.Header{}
	h.Add("X-Test", "a")
	h.Add("X-Test", "b")
	h.Set("Single", "one")

	out := authzHeaders(h)
	if !slices.Equal(out["x-test"], []string{"a", "b"}) {
		t.Errorf("expected x-test=[a b], got %q", out["x-test"])
	}
	if !slices.Equal(out["single"], []string{"one"}) {
		t.Errorf("expected single=[one], got %q", out["single"])
	}
}

// authzAllow returns cfg.FailOpen when payload marshaling fails (jsonMarshal seam).
func TestAuthzAllow_MarshalError_FailOpen(t *testing.T) {
	orig := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) { return nil, io.ErrUnexpectedEOF }
	defer func() { jsonMarshal = orig }()

	cfg := config.ExtAuthzConfig{
		Endpoint:  "http://127.0.0.1:1/authz",
		Timeout:   config.DurationOf(50 * time.Millisecond),
		Transport: "http",
		FailOpen:  true,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	client := authzClient(cfg)
	if !authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=true (fail_open) on marshal error")
	}
}

// authzRequestURL with a unix:// endpoint returns http://localhost + path.
func TestAuthzRequestURL_UnixSocket_WithPath(t *testing.T) {
	cfg := config.ExtAuthzConfig{
		Endpoint: "unix:///var/run/authz.sock",
		Path:     "/authz/allow",
	}
	got := authzRequestURL(cfg)
	want := "http://localhost/authz/allow"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// authzRequestURL with a unix:// endpoint and empty Path uses "/".
func TestAuthzRequestURL_UnixSocket_EmptyPath(t *testing.T) {
	cfg := config.ExtAuthzConfig{
		Endpoint: "unix:///var/run/authz.sock",
		Path:     "",
	}
	got := authzRequestURL(cfg)
	want := "http://localhost/"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// authzRequestURL with an http:// endpoint returns the endpoint unchanged.
func TestAuthzRequestURL_HTTP_ReturnsEndpoint(t *testing.T) {
	cfg := config.ExtAuthzConfig{
		Endpoint: "http://authz-service:8080/allow",
	}
	got := authzRequestURL(cfg)
	if got != cfg.Endpoint {
		t.Errorf("expected %q, got %q", cfg.Endpoint, got)
	}
}

// authzClient with a unix:// endpoint returns a non-nil client with a custom transport.
func TestAuthzClient_UnixSocket_ReturnsClient(t *testing.T) {
	cfg := config.ExtAuthzConfig{Endpoint: "unix:///tmp/authz.sock"}
	c := authzClient(cfg)
	if c == nil {
		t.Fatal("expected non-nil *http.Client for unix socket endpoint")
	}
	if c.Transport == nil {
		t.Error("expected custom Transport for unix socket endpoint")
	}
}

// authzClient unix: the DialContext closure body is covered by invoking it
// against a nonexistent socket path. The connection fails but must not panic.
func TestAuthzClient_UnixSocket_DialContextInvoked(t *testing.T) {
	cfg := config.ExtAuthzConfig{Endpoint: "unix:///tmp/nonexistent-test-keel-authz.sock"}
	c := authzClient(cfg)
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport for unix socket client")
	}
	// Invoke the DialContext closure directly — it will fail (no socket) but must not panic.
	conn, err := tr.DialContext(context.Background(), "tcp", "localhost:0")
	if err == nil {
		conn.Close()
		t.Error("expected error dialing nonexistent unix socket")
	}
}

// authzClient with an http:// endpoint returns the default http.Client.
func TestAuthzClient_HTTP_ReturnsDefaultClient(t *testing.T) {
	cfg := config.ExtAuthzConfig{Endpoint: "http://authz-service:8080/allow"}
	c := authzClient(cfg)
	if c == nil {
		t.Error("expected non-nil *http.Client for http endpoint")
	}
}

// authzAllow returns cfg.FailOpen when the server is unreachable (network error).
func TestAuthzAllow_NetworkError_FailClosed(t *testing.T) {
	cfg := config.ExtAuthzConfig{
		Endpoint:  "http://127.0.0.1:1/authz",
		Timeout:   config.DurationOf(50 * time.Millisecond),
		Transport: "http",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	client := authzClient(cfg)

	if authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=false on network error with fail_open=false")
	}
}

func TestAuthzAllow_NetworkError_FailOpen(t *testing.T) {
	cfg := config.ExtAuthzConfig{
		Endpoint:  "http://127.0.0.1:1/authz",
		Timeout:   config.DurationOf(50 * time.Millisecond),
		Transport: "http",
		FailOpen:  true,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	client := authzClient(cfg)

	if !authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=true on network error with fail_open=true")
	}
}

// authzAllow returns false when the OPA response status is not 200.
func TestAuthzAllow_OPA_Non200_ReturnsFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "opa",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	client := authzClient(cfg)

	if authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=false for OPA 500 response")
	}
}

// authzAllow returns true when OPA responds 200 with {"result":true}.
func TestAuthzAllow_OPA_Allow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":true}`))
	}))
	defer srv.Close()

	cfg := config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "opa",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	client := authzClient(cfg)

	if !authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=true for OPA 200 result:true")
	}
}

// authzAllow returns false when OPA responds 200 with {"result":false}.
func TestAuthzAllow_OPA_Deny(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":false}`))
	}))
	defer srv.Close()

	cfg := config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "opa",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	client := authzClient(cfg)

	if authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=false for OPA 200 result:false")
	}
}

// authzAllow returns false when OPA responds 200 with unparseable JSON body.
func TestAuthzAllow_OPA_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	cfg := config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "opa",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	client := authzClient(cfg)

	if authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=false for OPA 200 bad JSON")
	}
}

// authzAllow with http transport returns true when server responds 200.
func TestAuthzAllow_HTTP_Allow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "http",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	client := authzClient(cfg)

	if !authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=true for HTTP 200 response")
	}
}

// authzAllow returns cfg.FailOpen when the request URL is invalid (build fail).
func TestAuthzAllow_InvalidEndpointURL_FailClosed(t *testing.T) {
	// An endpoint with control characters makes http.NewRequestWithContext fail.
	cfg := config.ExtAuthzConfig{
		Endpoint:  "http://\x00invalid",
		Timeout:   config.DurationOf(50 * time.Millisecond),
		Transport: "http",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	client := authzClient(cfg)

	if authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=false on build fail with fail_open=false")
	}
}

// authzAllow returns cfg.FailOpen (true) when the request URL is invalid and fail_open=true.
func TestAuthzAllow_InvalidEndpointURL_FailOpen(t *testing.T) {
	cfg := config.ExtAuthzConfig{
		Endpoint:  "http://\x00invalid",
		Timeout:   config.DurationOf(50 * time.Millisecond),
		Transport: "http",
		FailOpen:  true,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	client := authzClient(cfg)

	if !authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=true on build fail with fail_open=true")
	}
}

// authzAllow with http transport returns false when server responds 403.
func TestAuthzAllow_HTTP_Deny(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "http",
		FailOpen:  false,
	}
	log := logging.New(logging.Config{Out: io.Discard})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	client := authzClient(cfg)

	if authzAllow(req, cfg, client, log) {
		t.Error("expected authzAllow=false for HTTP 403 response")
	}
}

func FuzzAuthzParseOPA(f *testing.F) {
	f.Add(200, `{"result":true}`)
	f.Add(200, `{"result":"true"}`)
	f.Add(200, `{"result":true`)
	f.Add(403, `{"result":true}`)
	log := logging.New(logging.Config{Out: io.Discard})
	f.Fuzz(func(t *testing.T, status int, body string) {
		resp := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
		if authzParseOPA(resp, log) && status != http.StatusOK {
			t.Fatalf("authzParseOPA allowed a non-200 response (status %d)", status)
		}
	})
}

func FuzzAuthzHeaders(f *testing.F) {
	f.Add("Authorization", "Bearer x", "X-Forwarded-For", "10.0.0.1")
	f.Add("", "", "", "")
	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		h := http.Header{}
		h.Add(k1, v1)
		h.Add(k2, v2)
		for k := range authzHeaders(h) {
			if k != strings.ToLower(k) {
				t.Fatalf("authzHeaders kept non-lowercase key %q", k)
			}
		}
	})
}

// Class D: one field line holding "ok,blocked" and two lines "ok" + "blocked" reach
// the policy as different inputs, so a policy can tell them apart.
func TestAuthzHeaders_FieldLinesStayDistinct(t *testing.T) {
	one := http.Header{}
	one.Add("X-Role", "ok,blocked")
	two := http.Header{}
	two.Add("X-Role", "ok")
	two.Add("X-Role", "blocked")

	a, err := json.Marshal(authzHeaders(one))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(authzHeaders(two))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatalf("policy input is identical for one and two field lines: %s", a)
	}
	blocked := func(h map[string][]string) bool {
		for _, v := range h["x-role"] {
			if v == "blocked" {
				return true
			}
		}
		return false
	}
	if blocked(authzHeaders(one)) || !blocked(authzHeaders(two)) {
		t.Fatal(`a policy denying the role "blocked" must allow "ok,blocked" and deny "ok" + "blocked"`)
	}
}

// HH6: the policy input carries a hostile header value as one JSON string.
func TestAuthzPayload_ClientValuesStayOneElement(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header["X-Hostile"] = []string{hostile}
	b, err := json.Marshal(authzPayload(req, "opa"))
	if err != nil {
		t.Fatal(err)
	}
	var in struct {
		Input struct {
			Headers map[string][]string `json:"headers"`
		} `json:"input"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	if v := in.Input.Headers["x-hostile"]; len(v) != 1 || v[0] != hostile {
		t.Fatalf("x-hostile = %q, want one element equal to the client value", v)
	}
}

func TestAuthzClient_UnixSocketDoesNotFollowRedirects(t *testing.T) {
	if authzClient(config.ExtAuthzConfig{Endpoint: "unix:///tmp/opa.sock"}).CheckRedirect == nil {
		t.Fatal("unix-socket authz client follows redirects")
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

// ExtAuthz allows the request through to next when the decision endpoint permits it.
func TestExtAuthz_Allow_CallsNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{ExtAuthz: config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "http",
	}}
	log := logging.New(logging.Config{Out: io.Discard})
	called := false
	h := ExtAuthz(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	h.ServeHTTP(rr, req)

	if !called || rr.Code != http.StatusOK {
		t.Errorf("expected allow → next called with 200, got called=%v code=%d", called, rr.Code)
	}
}

// ExtAuthz returns 403 and does not call next when the decision endpoint denies.
func TestExtAuthz_Deny_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := config.Config{ExtAuthz: config.ExtAuthzConfig{
		Endpoint:  srv.URL,
		Timeout:   config.DurationOf(2 * time.Second),
		Transport: "http",
	}}
	log := logging.New(logging.Config{Out: io.Discard})
	called := false
	h := ExtAuthz(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), log)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	h.ServeHTTP(rr, req)

	if called {
		t.Error("expected next NOT to be called on deny")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 on deny, got %d", rr.Code)
	}
}

// A redirect from the decision endpoint is not followed and is not an allow.
func TestExtAuthz_RedirectNotFollowed(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { followed.Store(true) }))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	if code := serveAuthz(t, authzCfg(redirector.URL, "http")); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a redirecting authz endpoint", code)
	}
	if followed.Load() {
		t.Fatal("authz client followed the redirect")
	}
}

// An oversized OPA decision body is not read in full and does not allow.
func TestExtAuthz_OPABodyCapped(t *testing.T) {
	opa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"pad":"`+strings.Repeat("x", 2*maxAuthzResponseBytes)+`","result":true}`)
	}))
	t.Cleanup(opa.Close)
	if code := serveAuthz(t, authzCfg(opa.URL, "opa")); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an oversized OPA body", code)
	}
}

func serveAuthz(t *testing.T, cfg config.Config) int {
	t.Helper()
	h := ExtAuthz(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), logging.New(logging.Config{Out: io.Discard}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code
}

func authzCfg(endpoint, transport string) config.Config {
	var cfg config.Config
	cfg.ExtAuthz = config.ExtAuthzConfig{
		Enabled: true, Endpoint: endpoint, Transport: transport,
		Timeout: config.DurationOf(2 * time.Second),
	}
	return cfg
}

var authzLog = logging.New(logging.Config{JSON: false})

func authzCfgFailOpen(endpoint, transport string, failOpen bool) config.Config {
	return config.Config{
		ExtAuthz: config.ExtAuthzConfig{
			Enabled:   true,
			Endpoint:  endpoint,
			Timeout:   config.DurationOf(2 * time.Second),
			Transport: transport,
			FailOpen:  failOpen,
		},
	}
}

func authzMiddleware(endpoint, transport string, failOpen bool) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return ExtAuthz(authzCfgFailOpen(endpoint, transport, failOpen), inner, authzLog)
}

func TestExtAuthz_Allow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware(srv.URL, "http", false).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestExtAuthz_Deny(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware(srv.URL, "http", false).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestExtAuthz_FailClosed(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware("http://127.0.0.1:1", "http", false).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on unreachable endpoint with fail_open=false, got %d", rr.Code)
	}
}

func TestExtAuthz_FailOpen(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware("http://127.0.0.1:1", "http", true).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on unreachable endpoint with fail_open=true, got %d", rr.Code)
	}
}

func TestExtAuthz_OPA_Allow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":true}`)
	}))
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware(srv.URL, "opa", false).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for OPA result=true, got %d", rr.Code)
	}
}

func TestExtAuthz_OPA_Deny(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":false}`)
	}))
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware(srv.URL, "opa", false).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for OPA result=false, got %d", rr.Code)
	}
}

func TestExtAuthz_OPA_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `not-json`)
	}))
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware(srv.URL, "opa", false).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for malformed OPA response, got %d", rr.Code)
	}
}

func TestExtAuthz_PayloadStructure_HTTP(t *testing.T) {
	// Per docs/security.md §6.2: the HTTP transport sends a flat JSON object
	// with method, path, query, headers, and remote fields.
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/resource?page=1", nil)
	req.Header.Set("x-custom", "testval")
	req.RemoteAddr = "10.0.0.5:54321"
	authzMiddleware(srv.URL, "http", false).ServeHTTP(httptest.NewRecorder(), req)

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("authz payload not valid JSON: %v — body: %s", err, captured)
	}
	for _, key := range []string{"method", "path", "query", "headers", "remote"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("authz payload missing required field %q", key)
		}
	}
	if got := payload["method"]; got != "GET" {
		t.Errorf("method: got %v, want GET", got)
	}
	if got := payload["path"]; got != "/api/resource" {
		t.Errorf("path: got %v, want /api/resource", got)
	}
	if got := payload["query"]; got != "page=1" {
		t.Errorf("query: got %v, want page=1", got)
	}
}

func TestExtAuthz_OPA_PayloadWrapped(t *testing.T) {
	// Per docs/security.md §6.2: the OPA transport wraps the envelope in
	// {"input": ...} per OPA convention.
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":true}`)
	}))
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	authzMiddleware(srv.URL, "opa", false).ServeHTTP(httptest.NewRecorder(), req)

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("OPA payload not valid JSON: %v — body: %s", err, captured)
	}
	if _, ok := payload["input"]; !ok {
		t.Error("OPA payload missing top-level 'input' wrapper per OPA convention")
	}
	inner, ok := payload["input"].(map[string]any)
	if !ok {
		t.Fatal("OPA 'input' field is not a JSON object")
	}
	for _, key := range []string{"method", "path", "query", "headers", "remote"} {
		if _, ok := inner[key]; !ok {
			t.Errorf("OPA input missing required field %q", key)
		}
	}
}

func TestExtAuthz_Timeout_FailClosed(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	cfg := config.Config{
		ExtAuthz: config.ExtAuthzConfig{
			Enabled:   true,
			Endpoint:  srv.URL,
			Timeout:   config.DurationOf(50 * time.Millisecond),
			Transport: "http",
			FailOpen:  false,
		},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := ExtAuthz(cfg, inner, authzLog)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on timeout with fail_open=false, got %d", rr.Code)
	}
}
