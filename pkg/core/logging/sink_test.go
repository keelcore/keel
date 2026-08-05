//go:build !no_remotelog && !windows

package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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
