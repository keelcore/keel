// pkg/core/router/router_test.go
package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// Handler — port not found (pm == nil)
// ---------------------------------------------------------------------------

// Handler returns 404 when no handlers are registered on the port derived
// from the request.  requestPort falls back to ports.HTTP (8080) when there
// is no LocalAddr context key, no Host header, and no absolute URL.
func TestHandler_NilPortMux_Returns404(t *testing.T) {
	r := New()
	// Register a handler on a different port so the router is non-empty.
	r.Handle(9999, "/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Build a request that will resolve to port 8080 (the fallback) — which
	// has no registered handlers.
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/ping", nil)
	req.Host = "localhost:8080"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unregistered port, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Handler — port found, mux registered, handler called
// ---------------------------------------------------------------------------

func TestHandler_RegisteredPort_CallsHandler(t *testing.T) {
	r := New()
	r.Handle(9090, "/hello", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://localhost:9090/hello", nil)
	req.Host = "localhost:9090"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Has — port not yet created
// ---------------------------------------------------------------------------

func TestHas_MissingPort_ReturnsFalse(t *testing.T) {
	r := New()
	if r.Has(1234, "/foo") {
		t.Error("expected Has to return false for unregistered port")
	}
}

// ---------------------------------------------------------------------------
// getOrCreatePortMux — second call returns existing portMux
// ---------------------------------------------------------------------------

func TestGetOrCreatePortMux_IdempotentForSamePort(t *testing.T) {
	r := New()
	pm1 := r.getOrCreatePortMux(5555)
	pm2 := r.getOrCreatePortMux(5555)
	if pm1 != pm2 {
		t.Error("expected same *portMux on second call for same port")
	}
}

// ---------------------------------------------------------------------------
// DefaultRegistrar — registers "/" on ports.HTTP and returns keel: ok
// ---------------------------------------------------------------------------

func TestDefaultRegistrar_RegistersRoot(t *testing.T) {
	r := New()
	DefaultRegistrar().Register(r)

	// ports.HTTP == 8080; use a request with Host: localhost:8080
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	req.Host = "localhost:8080"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from DefaultRegistrar root, got %d", rr.Code)
	}
}

// DefaultRegistrar returns 404 for non-root paths.
func TestDefaultRegistrar_NonRoot_Returns404(t *testing.T) {
	r := New()
	DefaultRegistrar().Register(r)

	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/other", nil)
	req.Host = "localhost:8080"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-root path, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// requestPort — URL.Host fallback branch
// ---------------------------------------------------------------------------

// Handler resolves the port from req.URL.Host when req.Host is empty and
// there is no LocalAddrContextKey — exercises the absolute-URL fallback.
func TestHandler_URLHostFallback_CallsHandler(t *testing.T) {
	r := New()
	r.Handle(7777, "/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	// Build request with explicit URL.Host so requestPort uses the URL branch.
	req := httptest.NewRequest(http.MethodGet, "http://example.com:7777/ping", nil)
	req.Host = "" // force the URL.Host fallback path

	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202 via URL.Host fallback, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Handle — duplicate route overwrites the previous handler
// ---------------------------------------------------------------------------

// Registering the same pattern twice on the same port: second handler wins.
func TestHandle_DuplicateRoute_OverwritesPrevious(t *testing.T) {
	r := New()
	r.Handle(6666, "/test", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // first handler
	}))
	r.Handle(6666, "/test", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated) // second handler overwrites
	}))

	req := httptest.NewRequest(http.MethodGet, "http://localhost:6666/test", nil)
	req.Host = "localhost:6666"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	// The mux is rebuilt on each Handle call; the last handler wins.
	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201 from second (overwriting) handler, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// requestPort — Host header without port (falls back to ports.HTTP)
// ---------------------------------------------------------------------------

// Handler falls back to ports.HTTP (8080) when the Host header has no port.
func TestHandler_HostNoPort_FallbackToHTTP(t *testing.T) {
	r := New()
	// Register something on 8080 to confirm the fallback port is used.
	r.Handle(8080, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "example.com" // no port → SplitHostPort fails → falls back to ports.HTTP
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 on fallback to ports.HTTP, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Handler — port found but mux is nil (white-box: directly construct portMux)
// ---------------------------------------------------------------------------

// Handler returns 404 when pm is non-nil but its mux pointer is nil.
// This exercises the m == nil branch (line 83-86 in router.go).
func TestHandler_NilMux_Returns404(t *testing.T) {
	r := New()
	// Insert a portMux with a nil mux via direct struct manipulation.
	r.mu.Lock()
	r.ports[2345] = &portMux{} // mux.atomic.Pointer zero value is nil
	r.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "http://localhost:2345/foo", nil)
	req.Host = "localhost:2345"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nil mux, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// RegistrarFunc — Register delegates to the function
// ---------------------------------------------------------------------------

func TestRegistrarFunc_Register_Delegates(t *testing.T) {
	r := New()
	called := false
	var rf RegistrarFunc = func(rt *Router) {
		called = true
		rt.Handle(4444, "/rf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}
	rf.Register(r)
	if !called {
		t.Error("expected RegistrarFunc.Register to call the underlying function")
	}
	if !r.Has(4444, "/rf") {
		t.Error("expected route /rf on port 4444 after RegistrarFunc.Register")
	}
}

// fakeAddr is a net.Addr whose String() carries a host:port used by
// requestPort's LocalAddrContextKey branch.
type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }

func (a fakeAddr) String() string { return a.s }

// TestRequestPort_LocalAddrContextKey covers the LocalAddrContextKey branch in
// requestPort (lines 118-124) by injecting a net.Addr into the request context.
func TestRequestPort_LocalAddrContextKey(t *testing.T) {
	r := New()
	r.Handle(5599, "/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Host = "" // ensure the LocalAddr branch, not the Host branch, resolves the port
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, fakeAddr{s: "127.0.0.1:5599"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202 via LocalAddrContextKey branch, got %d", rr.Code)
	}
}

// TestHandle_NilRegs_Initializes covers the pm.regs == nil branch in Handle
// (lines 55-57). getOrCreatePortMux always allocates regs, so the branch is
// reachable only by inserting a portMux with a nil regs map directly.
func TestHandle_NilRegs_Initializes(t *testing.T) {
	r := New()
	r.mu.Lock()
	r.ports[3456] = &portMux{} // regs is nil, mux pointer is nil
	r.mu.Unlock()

	r.Handle(3456, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	if !r.Has(3456, "/x") {
		t.Fatal("expected /x registered after Handle initialized nil regs")
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost:3456/x", nil)
	req.Host = "localhost:3456"
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from handler registered on port with nil regs, got %d", rr.Code)
	}
}

// TestGetOrCreatePortMux_DoubleCheck deterministically covers the second
// nil-check under the write lock in getOrCreatePortMux (the double-checked-lock
// "loser" path). The raceHook seam parks a caller between the lock-free read and
// the write-lock acquisition; while it is parked, this goroutine creates the
// port's mux, so the parked caller's post-lock re-read observes it as already
// present and returns the same singleton.
func TestGetOrCreatePortMux_DoubleCheck(t *testing.T) {
	r := New()
	const port = 40000

	reached := make(chan struct{})
	release := make(chan struct{})
	var parked atomic.Bool
	orig := raceHook
	raceHook = func() {
		// Only the first caller parks; later callers (the creator below) pass
		// straight through so they do not deadlock on the same hook.
		if parked.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
	}
	t.Cleanup(func() { raceHook = orig })

	loser := make(chan *portMux, 1)
	go func() {
		loser <- r.getOrCreatePortMux(port) // passes the first nil-check, then parks
	}()

	<-reached                            // parked between the read and the write lock
	winner := r.getOrCreatePortMux(port) // create the mux while the loser is parked
	close(release)                       // let the loser proceed into the double-check

	if got := <-loser; got != winner {
		t.Fatalf("double-check must return the existing portMux: got %p, want %p", got, winner)
	}
}
