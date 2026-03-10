//go:build !no_authz

package unit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

var authzLog = logging.New(logging.Config{JSON: false})

func authzCfg(endpoint, transport string, failOpen bool) config.Config {
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
	return mw.ExtAuthz(authzCfg(endpoint, transport, failOpen), inner, authzLog)
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
	h := mw.ExtAuthz(cfg, inner, authzLog)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on timeout with fail_open=false, got %d", rr.Code)
	}
}
