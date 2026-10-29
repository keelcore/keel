package mw

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

// stubMemory sets the effective limit and the bytes Go holds (Sys−HeapReleased).
func stubMemory(t *testing.T, limit int64, held uint64) {
	t.Helper()
	origLimit, origRead := memLimit, readMemStats
	t.Cleanup(func() { memLimit, readMemStats = origLimit, origRead })
	memLimit = func() int64 { return limit }
	readMemStats = func(ms *runtime.MemStats) { ms.Sys = held; ms.HeapReleased = 0 }
}

func guardLog() *logging.Logger { return logging.New(logging.Config{Out: io.Discard}) }

func TestShedOnLowMemory(t *testing.T) {
	g := NewMemoryGuard()
	h := ShedOnLowMemory(g, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("not low: status %d, want 200", rec.Code)
	}
	g.low.Store(true)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("low: status %d Retry-After %q, want 503 / 1", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// Below the floor it sheds; it stays shedding until headroom reaches 2× the floor.
func TestMemoryGuard_FloorAndHysteresis(t *testing.T) {
	const floor = 10 << 20
	g := NewMemoryGuard()
	stubMemory(t, 100<<20, 95<<20) // headroom 5 MiB < floor
	g.sample(floor, guardLog())
	if !g.Low() {
		t.Fatal("headroom below floor did not shed")
	}
	stubMemory(t, 100<<20, 85<<20) // headroom 15 MiB: above floor, below 2× floor
	g.sample(floor, guardLog())
	if !g.Low() {
		t.Fatal("recovered before headroom reached 2× floor (no hysteresis)")
	}
	stubMemory(t, 100<<20, 70<<20) // headroom 30 MiB ≥ 2× floor
	g.sample(floor, guardLog())
	if g.Low() {
		t.Fatal("did not recover at 2× floor")
	}
}

// With no memory limit known there is nothing to measure against: never shed.
func TestMemoryGuard_NoLimitNeverSheds(t *testing.T) {
	g := NewMemoryGuard()
	g.low.Store(true)
	stubMemory(t, math.MaxInt64, 1<<40)
	g.sample(10<<20, guardLog())
	if g.Low() {
		t.Fatal("shed with no memory limit known")
	}
}

func TestRunMemoryGuard_DisabledFloor(t *testing.T) {
	g := NewMemoryGuard()
	g.low.Store(true)
	RunMemoryGuard(context.Background(), g, 0, guardLog()) // returns immediately
	if g.Low() {
		t.Fatal("disabled guard left shedding on")
	}
}

// The loop samples on its ticker and clears the guard when it exits.
func TestRunMemoryGuard_LoopSamplesAndClearsOnExit(t *testing.T) {
	stubMemory(t, 100<<20, 99<<20)
	g := NewMemoryGuard()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { RunMemoryGuard(ctx, g, 10<<20, guardLog()); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for !g.Low() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !g.Low() {
		t.Fatal("loop never sampled low headroom")
	}
	cancel()
	<-done
	if g.Low() {
		t.Fatal("guard still shedding after the loop exited")
	}
}

// The real memLimit reads the effective limit without changing it.
func TestMemLimit_ReadsEffectiveLimit(t *testing.T) {
	if got := memLimit(); got <= 0 {
		t.Fatalf("memLimit() = %d, want a positive limit (MaxInt64 when unset)", got)
	}
}
