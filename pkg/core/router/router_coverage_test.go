// pkg/core/router/router_coverage_test.go
package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
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

// TestGetOrCreatePortMux_ConcurrentDoubleCheck covers the second nil-check
// under the write lock in getOrCreatePortMux (lines 102-104). Many goroutines
// race on the same fresh port: the loser(s) pass the initial RLock nil-check,
// then find the portMux already created when they acquire the write lock.
//
// The double-check executes only when at least two goroutines observe the port
// as nil before any of them completes creation, so it is inherently
// probabilistic. Many rounds with a wide, simultaneously released fan-out make
// the hit effectively certain within the run.
func TestGetOrCreatePortMux_ConcurrentDoubleCheck(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(0))
	if runtime.GOMAXPROCS(0) < 4 {
		runtime.GOMAXPROCS(4)
	}

	const goroutines = 256
	for round := 0; round < 400; round++ {
		r := New()
		port := 40000 + round
		barrier := make(chan struct{})
		var done sync.WaitGroup
		done.Add(goroutines)
		results := make([]*portMux, goroutines)
		for i := range goroutines {
			go func(idx int) {
				defer done.Done()
				<-barrier // release all goroutines simultaneously
				results[idx] = r.getOrCreatePortMux(port)
			}(i)
		}
		close(barrier)
		done.Wait()

		// All goroutines must observe the same singleton portMux.
		first := results[0]
		for i, pm := range results {
			if pm != first {
				t.Fatalf("goroutine %d got a different portMux for port %d", i, port)
			}
		}
	}
}
