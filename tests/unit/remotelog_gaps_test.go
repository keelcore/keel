//go:build !no_remotelog

// tests/unit/remotelog_gaps_test.go
package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

// Run flushes a batch immediately when it accumulates 100 lines.
func TestRemoteLog_Run_FlushesLargeBatch(t *testing.T) {
	received := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		select {
		case received <- 1:
		default:
		}
	}))
	defer srv.Close()

	// Large flush interval so only the 100-line batch trigger fires, not the ticker.
	sink := logging.NewHTTPSink(srv.URL, 200, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	for i := 0; i < 100; i++ {
		_, _ = sink.Write([]byte("line\n"))
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Error("batch of 100 lines was not flushed within 2s")
	}
}

// post: http.NewRequest fails for an invalid endpoint URL.
func TestRemoteLog_Post_BadURL(t *testing.T) {
	sink := logging.NewHTTPSink("://invalid-url", 10, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Write one line then let Run process it; post should silently return on NewRequest error.
	_, _ = sink.Write([]byte("line\n"))
	sink.Run(ctx) // blocks until ctx done; must not panic
}

// post: client.Do fails when the server has been shut down.
func TestRemoteLog_Post_ServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close() // shut down before post is called

	sink := logging.NewHTTPSink(url, 10, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, _ = sink.Write([]byte("line\n"))
	sink.Run(ctx) // post will fail client.Do; must not panic
}

// Run drain loop: lines remaining in buf when ctx is cancelled are drained.
// ctx is pre-cancelled so select can choose ctx.Done() before all lines
// are consumed by the outer loop; the drain inner select then processes the rest.
func TestRemoteLog_Run_DrainLoopProcessesRemaining(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Large capacity and huge flush interval; no ticker fires during the test.
	sink := logging.NewHTTPSink(srv.URL, 200, time.Hour)

	// Pre-fill buf with 50 lines, then cancel ctx before calling Run.
	// With ctx already done the outer select has two ready channels; Go picks
	// randomly, so on most iterations the drain branch is reached while lines
	// are still in the buf, covering the inner "case line" branch.
	for i := 0; i < 50; i++ {
		_, _ = sink.Write([]byte("line\n"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	sink.Run(ctx) // must return; does not block
}