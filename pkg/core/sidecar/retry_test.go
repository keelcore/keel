//go:build !no_sidecar

package sidecar

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
)

var errRefused = errors.New("connection refused")

// scriptedDial fails the first `fails` calls, then returns a pipe conn.
type scriptedDial struct {
	mu    sync.Mutex
	fails int
	calls int
}

func (s *scriptedDial) dial(context.Context, string, string) (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.fails {
		return nil, errRefused
	}
	c, _ := net.Pipe()
	return c, nil
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newBufLogger() (*logging.Logger, *syncBuffer) {
	var b syncBuffer
	return logging.New(logging.Config{JSON: true, Out: &b}), &b
}

func TestRetryDial_SingleAttemptIsUnwrapped(t *testing.T) {
	s := &scriptedDial{fails: 1}
	d := retryDial(s.dial, 1, time.Millisecond, Hooks{})
	if _, err := d(context.Background(), "tcp", "x:1"); !errors.Is(err, errRefused) {
		t.Fatalf("err = %v, want refused", err)
	}
	if s.calls != 1 {
		t.Fatalf("calls = %d, want 1", s.calls)
	}
}

func TestRetryDial_SucceedsAfterRetries(t *testing.T) {
	log, out := newBufLogger()
	s := &scriptedDial{fails: 2}
	d := retryDial(s.dial, 3, time.Millisecond, Hooks{Log: log})
	conn, err := d(context.Background(), "tcp", "x:1")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	if s.calls != 3 {
		t.Fatalf("calls = %d, want 3", s.calls)
	}
	if n := strings.Count(out.String(), `"msg":"upstream_dial_retry"`); n != 2 {
		t.Fatalf("retry log lines = %d, want 2:\n%s", n, out.String())
	}
	if strings.Contains(out.String(), "upstream_dial_failed") {
		t.Fatalf("unexpected upstream_dial_failed:\n%s", out.String())
	}
}

func TestRetryDial_ExhaustedReturnsLastError(t *testing.T) {
	log, out := newBufLogger()
	s := &scriptedDial{fails: 10}
	d := retryDial(s.dial, 3, time.Millisecond, Hooks{Log: log})
	if _, err := d(context.Background(), "tcp", "x:1"); !errors.Is(err, errRefused) {
		t.Fatalf("err = %v, want refused", err)
	}
	if s.calls != 3 {
		t.Fatalf("calls = %d, want 3", s.calls)
	}
	if !strings.Contains(out.String(), `"msg":"upstream_dial_failed"`) {
		t.Fatalf("missing upstream_dial_failed:\n%s", out.String())
	}
}

func TestRetryDial_StopsWhenContextEndsDuringBackoff(t *testing.T) {
	s := &scriptedDial{fails: 10}
	d := retryDial(s.dial, 5, time.Hour, Hooks{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := d(ctx, "tcp", "x:1"); !errors.Is(err, errRefused) {
		t.Fatalf("err = %v, want refused", err)
	}
	if time.Since(start) > 5*time.Second || s.calls != 1 {
		t.Fatalf("calls = %d after %s; want 1 and a prompt return", s.calls, time.Since(start))
	}
}

func TestRetryDial_StopsWhenContextAlreadyDone(t *testing.T) {
	s := &scriptedDial{fails: 10}
	d := retryDial(s.dial, 5, time.Millisecond, Hooks{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d(ctx, "tcp", "x:1"); err == nil {
		t.Fatal("expected an error")
	}
	if s.calls != 1 {
		t.Fatalf("calls = %d, want 1", s.calls)
	}
}

func TestBaseDial(t *testing.T) {
	s := &scriptedDial{}
	if conn, err := baseDial(s.dial)(context.Background(), "tcp", "x:1"); err != nil || s.calls != 1 {
		t.Fatalf("custom dial not used: err %v calls %d", err, s.calls)
	} else {
		_ = conn.Close()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conn, err := baseDial(nil)(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("default dial: %v", err)
	}
	_ = conn.Close()
}

// When keel signs, the client's Authorization is replaced by keel's; a signing
// failure is a 502 and the request never reaches the upstream unsigned.
func TestNew_Signing_ReplacesClientAuthAndFailsClosed(t *testing.T) {
	var gotAuth string
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotAuth = r.Header.Get("Authorization")
	}))
	defer upstream.Close()
	cfg := config.Config{Sidecar: config.SidecarConfig{UpstreamURL: upstream.URL}}

	signErr := error(nil)
	h, err := New(cfg, func(r *http.Request) error {
		if signErr != nil {
			return signErr
		}
		r.Header.Set("Authorization", "Bearer keel-token")
		return nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil)
		req.Header.Set("Authorization", "Bearer client-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call(); code != http.StatusOK || gotAuth != "Bearer keel-token" {
		t.Fatalf("signed: status %d auth %q", code, gotAuth)
	}
	signErr = errors.New("key unavailable")
	if code := call(); code != http.StatusBadGateway {
		t.Fatalf("sign failure: status %d, want 502", code)
	}
	if hits != 1 {
		t.Fatalf("upstream hit %d times, want 1 (unsigned request forwarded)", hits)
	}
}

func TestRetryAfter_RetriesOnceAfterTheDelay(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		rt := &scriptedRT{script: []*http.Response{reply(code, "0"), reply(http.StatusOK, "")}}
		var retries int
		tr := &retryAfterTransport{inner: rt, budget: time.Second, hooks: Hooks{RetryAfter: func() { retries++ }}}
		resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "http://u/x", nil))
		if err != nil {
			t.Fatalf("%d: %v", code, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(rt.sent) != 2 || retries != 1 {
			t.Fatalf("%d: status %d, sent %d, retries %d; want 200, 2, 1", code, resp.StatusCode, len(rt.sent), retries)
		}
		if rt.closed.Load() != 2 {
			t.Fatalf("%d: %d bodies closed, want 2 (the first before the retry)", code, rt.closed.Load())
		}
	}
}
