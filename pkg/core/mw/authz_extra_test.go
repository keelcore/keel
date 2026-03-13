//go:build !no_authz

package mw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

// ---------------------------------------------------------------------------
// authzRequestURL
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// authzClient
// ---------------------------------------------------------------------------

// authzClient with a unix:// endpoint returns a non-nil client with a custom transport.
func TestAuthzClient_UnixSocket_ReturnsClient(t *testing.T) {
	cfg := config.ExtAuthzConfig{Endpoint: "unix:///tmp/authz.sock"}
	c := authzClient(cfg)
	if c == nil {
		t.Error("expected non-nil *http.Client for unix socket endpoint")
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

// ---------------------------------------------------------------------------
// authzAllow — error paths
// ---------------------------------------------------------------------------

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
