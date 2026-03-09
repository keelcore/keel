package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/mw"
)

func TestConcurrencyLimit_Disabled(t *testing.T) {
	cfg := config.Config{} // MaxConcurrent = 0 → pass-through
	h := mw.ConcurrencyLimit(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestConcurrencyLimit_ImmediateRejectWhenFull(t *testing.T) {
	// max_concurrent=1, no queue: second concurrent request → 429.
	gate := make(chan struct{})
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 1}}
	h := mw.ConcurrencyLimit(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	time.Sleep(20 * time.Millisecond) // let goroutine acquire the slot

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	close(gate)
	wg.Wait()

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestConcurrencyLimit_QueuedRequestsComplete(t *testing.T) {
	// max_concurrent=1, queue_depth=2: all 3 requests must eventually complete.
	release := make(chan struct{}) // unbuffered: drives sequential release
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 1, QueueDepth: 2}}
	h := mw.ConcurrencyLimit(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	recorders := make([]*httptest.ResponseRecorder, 3)
	var wg sync.WaitGroup
	for i := range recorders {
		recorders[i] = httptest.NewRecorder()
		wg.Add(1)
		rr := recorders[i]
		go func() {
			defer wg.Done()
			h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
		}()
	}

	time.Sleep(20 * time.Millisecond) // let goroutines fill capacity
	for i := 0; i < 3; i++ {
		release <- struct{}{} // each send unblocks one handler
	}
	wg.Wait()

	for i, rr := range recorders {
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rr.Code)
		}
	}
}

func TestConcurrencyLimit_QueueOverflowRejects(t *testing.T) {
	// max_concurrent=1, queue_depth=2: (1+2+1)th request → 429.
	const max, depth = 1, 2
	gate := make(chan struct{})
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: max, QueueDepth: depth}}
	h := mw.ConcurrencyLimit(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	for i := 0; i < max+depth; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
	time.Sleep(30 * time.Millisecond) // let goroutines fill capacity

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	close(gate)
	wg.Wait()

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 when at capacity, got %d", rr.Code)
	}
}

func TestConcurrencyLimit_QueuedContextTimeout(t *testing.T) {
	// max_concurrent=1, queue_depth=5: queued request whose context expires → 503.
	gate := make(chan struct{})
	cfg := config.Config{Limits: config.LimitsConfig{MaxConcurrent: 1, QueueDepth: 5}}
	h := mw.ConcurrencyLimit(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	time.Sleep(20 * time.Millisecond) // let goroutine acquire slot

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req) // blocks until context expires

	close(gate)
	wg.Wait()

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for timed-out queued request, got %d", rr.Code)
	}
}
