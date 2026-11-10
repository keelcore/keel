//go:build !no_remotelog && !windows

package logging

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// roundTripFunc adapts a function to http.RoundTripper so post() can be
// exercised without a real network peer.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
	}
}

func TestNewHTTPSink(t *testing.T) {
	s := NewHTTPSink("http://example/logs", 8, time.Second)
	if s.endpoint != "http://example/logs" {
		t.Fatalf("endpoint = %q", s.endpoint)
	}
	if cap(s.buf) != 8 {
		t.Fatalf("buf cap = %d, want 8", cap(s.buf))
	}
	if s.flushInterval != time.Second {
		t.Fatalf("flushInterval = %v", s.flushInterval)
	}
	if s.client == nil {
		t.Fatal("nil client")
	}
}

func TestWriteBufferedAndDrop(t *testing.T) {
	// Buffered slot available → line enqueued.
	s := NewHTTPSink("http://x", 1, time.Second)
	n, err := s.Write([]byte("line1\n"))
	if err != nil || n != len("line1\n") {
		t.Fatalf("Write = (%d,%v)", n, err)
	}
	if s.DropsTotal() != 0 {
		t.Fatalf("unexpected drops: %d", s.DropsTotal())
	}
	// Second write with full buffer → dropped.
	if _, err := s.Write([]byte("line2\n")); err != nil {
		t.Fatalf("Write err: %v", err)
	}
	if s.DropsTotal() != 1 {
		t.Fatalf("drops = %d, want 1", s.DropsTotal())
	}
}

func TestRunBatchFlush(t *testing.T) {
	s := NewHTTPSink("http://x", 256, time.Hour) // long interval → no ticker flush
	got := make(chan struct{}, 4)
	s.client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		got <- struct{}{}
		return okResponse(), nil
	})

	// Enqueue a full batch before starting Run.
	for i := 0; i < httpSinkBatchSize; i++ {
		if _, err := s.Write([]byte("x\n")); err != nil {
			t.Fatalf("Write err: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("batch flush did not POST")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunTickerFlush(t *testing.T) {
	s := NewHTTPSink("http://x", 16, 2*time.Millisecond)
	got := make(chan struct{}, 4)
	s.client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		got <- struct{}{}
		return okResponse(), nil
	})

	if _, err := s.Write([]byte("a\n")); err != nil {
		t.Fatalf("Write err: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("ticker flush did not POST")
	}
}

func TestRunDrainOnCancel(t *testing.T) {
	s := NewHTTPSink("http://x", 128, time.Hour)
	posted := make(chan struct{}, 4)
	s.client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		posted <- struct{}{}
		return okResponse(), nil
	})

	// Pre-fill the buffer, then run with an already-cancelled context so the
	// ctx.Done drain path consumes the remaining lines and does a final flush.
	for i := 0; i < 64; i++ {
		if _, err := s.Write([]byte("y\n")); err != nil {
			t.Fatalf("Write err: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx) // returns once drained

	select {
	case <-posted:
	case <-time.After(2 * time.Second):
		t.Fatal("drain flush did not POST")
	}
}

func TestPostSuccess(t *testing.T) {
	s := NewHTTPSink("http://x", 1, time.Second)
	called := false
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.Header.Get("content-type") != "application/x-ndjson" {
			t.Errorf("missing content-type header")
		}
		return okResponse(), nil
	})
	s.post([][]byte{[]byte("l1\n"), []byte("l2\n")})
	if !called {
		t.Fatal("transport not invoked")
	}
}

func TestPostNewRequestError(t *testing.T) {
	// ":" is an invalid request URL → http.NewRequest returns an error.
	s := NewHTTPSink(":", 1, time.Second)
	called := false
	s.client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		called = true
		return okResponse(), nil
	})
	s.post([][]byte{[]byte("l\n")}) // must return before calling Do
	if called {
		t.Fatal("transport invoked despite NewRequest error")
	}
}

func TestPostDoError(t *testing.T) {
	s := NewHTTPSink("http://x", 1, time.Second)
	s.client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})
	// Should return cleanly without panicking on the nil response.
	s.post([][]byte{[]byte("l\n")})
}

// A batch the collector rejects (non-2xx) or never receives is counted as
// dropped, line by line.
func TestPost_FailedOrRejectedBatchCountsDrops(t *testing.T) {
	s := NewHTTPSink("http://example/logs", 8, time.Second)
	status := http.StatusServiceUnavailable
	s.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := okResponse()
		r.StatusCode = status
		return r, nil
	})}
	s.post([][]byte{[]byte("a\n"), []byte("b\n")})
	if got := s.DropsTotal(); got != 2 {
		t.Fatalf("drops after 503 = %d, want 2", got)
	}
	status = http.StatusAccepted
	s.post([][]byte{[]byte("c\n")})
	if got := s.DropsTotal(); got != 2 {
		t.Fatalf("drops after 202 = %d, want 2", got)
	}
}

func TestHTTPSink_UsesTheOutboundTLSPolicy(t *testing.T) {
	c := NewHTTPSink("https://logs.example", 1, time.Second).client
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("transport = %#v, want the outbound TLS policy", c.Transport)
	}
}

func TestRemoteLog_HTTPSink_ReceivesLogLines(t *testing.T) {
	var mu sync.Mutex
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, b...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, 100, 10*time.Millisecond)
	_, _ = sink.Write([]byte(`{"msg":"hello"}` + "\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go sink.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond) // let final flush complete

	mu.Lock()
	body := string(received)
	mu.Unlock()

	if !strings.Contains(body, "hello") {
		t.Errorf("expected log line in HTTP sink, got: %q", body)
	}
}

func TestRemoteLog_HTTPSink_DropsWhenFull(t *testing.T) {
	// bufCap=2: third write should be dropped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, 2, time.Hour) // huge flush interval → no auto-flush
	_, _ = sink.Write([]byte("line1\n"))
	_, _ = sink.Write([]byte("line2\n"))
	_, _ = sink.Write([]byte("line3\n")) // should be dropped

	if drops := sink.DropsTotal(); drops != 1 {
		t.Errorf("expected 1 drop, got %d", drops)
	}
}

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
	sink := NewHTTPSink(srv.URL, 200, time.Hour)
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
	sink := NewHTTPSink("://invalid-url", 10, time.Hour)
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

	sink := NewHTTPSink(url, 10, time.Hour)
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
	sink := NewHTTPSink(srv.URL, 200, time.Hour)

	// Pre-fill buf with 50 lines, then cancel ctx before calling Run.
	// With ctx already done the outer select has two ready channels; Go picks
	// randomly, so on most iterations the drain branch is reached while lines
	// are still in the buf, covering the inner "case line" branch.
	for i := 0; i < 50; i++ {
		_, _ = sink.Write([]byte("line\n"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()      // already cancelled
	sink.Run(ctx) // must return; does not block
}

func TestRemoteLog_HTTPSink_AttachesToLogger(t *testing.T) {
	// Verify that an HTTPSink used as an io.Writer receives logger output.
	var mu sync.Mutex
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, b...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, 100, 10*time.Millisecond)
	log := New(Config{Out: sink})
	log.Info("test-event", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go sink.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	body := string(received)
	mu.Unlock()

	if !strings.Contains(body, "test-event") {
		t.Errorf("expected test-event in sink, got: %q", body)
	}
}
