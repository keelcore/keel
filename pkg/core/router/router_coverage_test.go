// pkg/core/router/router_coverage_test.go
package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeAddr is a net.Addr whose String() carries a host:port used by
// requestPort's LocalAddrContextKey branch.
type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.s }

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
