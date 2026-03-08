// tests/unit/router_test.go
package unit

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/router"
)

// SPEC: collisions are scoped to (port, pattern) only.
// Two ports may register identical patterns without conflict.
func TestRouter_SamePatternDifferentPorts_NoCollision(t *testing.T) {
	r := router.New()

	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("p8080")) }))
	r.Handle(8443, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("p8443")) }))

	h := r.Handler()

	reqA := httptest.NewRequest("GET", "http://example.com/x", nil)
	reqA.Host = "example.com:8080"
	rrA := httptest.NewRecorder()
	h.ServeHTTP(rrA, reqA)

	reqB := httptest.NewRequest("GET", "http://example.com/x", nil)
	reqB.Host = "example.com:8443"
	rrB := httptest.NewRecorder()
	h.ServeHTTP(rrB, reqB)

	a, _ := io.ReadAll(rrA.Result().Body)
	b, _ := io.ReadAll(rrB.Result().Body)

	if string(a) != "p8080" {
		t.Fatalf("expected p8080, got %q", string(a))
	}
	if string(b) != "p8443" {
		t.Fatalf("expected p8443, got %q", string(b))
	}
}

// SPEC: last-write-wins should not require Host to include a port in unit tests IF
// the request is already known to target exactly one port.
// We enforce determinism by always setting Host in tests.
func TestRouter_LastWriteWins_SamePort_Simple(t *testing.T) {
	r := router.New()

	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("first")) }))
	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("second")) }))

	h := r.Handler()
	req := httptest.NewRequest("GET", "http://example.com/x", nil)
	req.Host = "example.com:8080"

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, string(body))
	}
	if !strings.Contains(string(body), "second") {
		t.Fatalf("expected last handler, got %q", string(body))
	}
}

// ---------------------------------------------------------------------------
// Has
// ---------------------------------------------------------------------------

func TestRouter_Has_UnknownPort(t *testing.T) {
	r := router.New()
	if r.Has(9999, "/x") {
		t.Error("Has should return false for unregistered port")
	}
}

func TestRouter_Has_UnknownPattern(t *testing.T) {
	r := router.New()
	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	if r.Has(8080, "/other") {
		t.Error("Has should return false for unregistered pattern")
	}
}

func TestRouter_Has_RegisteredPattern(t *testing.T) {
	r := router.New()
	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	if !r.Has(8080, "/x") {
		t.Error("Has should return true for registered (port, pattern)")
	}
}

// ---------------------------------------------------------------------------
// Handler: not-found paths
// ---------------------------------------------------------------------------

func TestRouter_Handler_UnknownPort_Returns404(t *testing.T) {
	r := router.New()
	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))

	h := r.Handler()
	req := httptest.NewRequest("GET", "http://example.com/x", nil)
	req.Host = "example.com:9999"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown port, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// requestPort: LocalAddrContextKey path
// ---------------------------------------------------------------------------

func TestRouter_RequestPort_LocalAddr(t *testing.T) {
	r := router.New()
	r.Handle(7777, "/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))

	h := r.Handler()
	req := httptest.NewRequest("GET", "/ping", nil)
	req.Host = ""

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7777}
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, addr)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || string(body) != "pong" {
		t.Errorf("expected 200 pong via LocalAddrContextKey, got %d %q", rr.Code, body)
	}
}

// ---------------------------------------------------------------------------
// requestPort: URL.Host fallback path
// ---------------------------------------------------------------------------

func TestRouter_RequestPort_URLHost(t *testing.T) {
	r := router.New()
	r.Handle(6666, "/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))

	h := r.Handler()
	req := httptest.NewRequest("GET", "http://example.com:6666/ping", nil)
	req.Host = ""

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || string(body) != "pong" {
		t.Errorf("expected 200 pong via URL.Host, got %d %q", rr.Code, body)
	}
}

// ---------------------------------------------------------------------------
// requestPort: fallback to ports.HTTP (8080) when no port can be determined
// ---------------------------------------------------------------------------

func TestRouter_RequestPort_Fallback_HTTP(t *testing.T) {
	r := router.New()
	r.Handle(8080, "/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))

	h := r.Handler()
	req := httptest.NewRequest("GET", "/ping", nil)
	req.Host = ""

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || string(body) != "pong" {
		t.Errorf("expected 200 pong via fallback port, got %d %q", rr.Code, body)
	}
}

// ---------------------------------------------------------------------------
// RegistrarFunc
// ---------------------------------------------------------------------------

func TestRegistrarFunc_Register(t *testing.T) {
	called := false
	fn := router.RegistrarFunc(func(_ *router.Router) { called = true })
	fn.Register(router.New())
	if !called {
		t.Error("RegistrarFunc.Register did not invoke the function")
	}
}

// ---------------------------------------------------------------------------
// DefaultRegistrar
// ---------------------------------------------------------------------------

func TestDefaultRegistrar_RootReturns200(t *testing.T) {
	r := router.New()
	router.DefaultRegistrar().Register(r)

	h := r.Handler()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com:8080"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Result().Body)
	if string(body) != "keel: ok\n" {
		t.Errorf("unexpected body: %q", string(body))
	}
}

func TestDefaultRegistrar_NonRootReturns404(t *testing.T) {
	r := router.New()
	router.DefaultRegistrar().Register(r)

	h := r.Handler()
	req := httptest.NewRequest("GET", "/other", nil)
	req.Host = "example.com:8080"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-root path, got %d", rr.Code)
	}
}
