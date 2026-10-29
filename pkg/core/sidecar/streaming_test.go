//go:build !no_sidecar

package sidecar

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

// proxyServer fronts upstream with the sidecar proxy on a real listener, so the
// test observes what a client actually receives (including aborted streams).
func proxyServer(t *testing.T, upstreamURL string, maxResp int64) *httptest.Server {
	t.Helper()
	h, err := New(config.Config{
		Sidecar:  config.SidecarConfig{UpstreamURL: upstreamURL},
		Security: config.SecurityConfig{MaxResponseBodyBytes: maxResp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// The proxy streams: the first chunk reaches the client while the upstream is
// still blocked mid-response, i.e. the body is never buffered whole.
func TestProxy_StreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "first-chunk\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second-chunk\n")
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })

	resp, err := http.Get(proxyServer(t, upstream.URL, 1<<20).URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "first-chunk\n" {
			t.Fatalf("first line = %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk not delivered while upstream was still writing — response is being buffered")
	}
}

// A chunked body (no Content-Length) of exactly the cap passes through intact.
func TestProxy_ChunkedBodyAtCapPasses(t *testing.T) {
	body := strings.Repeat("x", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush() // commit headers without Content-Length → chunked
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)

	resp, err := http.Get(proxyServer(t, upstream.URL, int64(len(body))).URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("body = %d bytes, want %d", len(got), len(body))
	}
}

// A chunked body over the cap is cut off mid-stream: the client never receives
// more than the cap and the stream ends in error rather than completing.
func TestProxy_ChunkedBodyOverCapAborts(t *testing.T) {
	const capBytes = 64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("y", 4*capBytes))
	}))
	t.Cleanup(upstream.Close)

	resp, err := http.Get(proxyServer(t, upstream.URL, capBytes).URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	got, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatalf("expected the oversized stream to be aborted, read %d bytes cleanly", len(got))
	}
	if len(got) > capBytes {
		t.Fatalf("client received %d bytes, cap is %d", len(got), capBytes)
	}
}

// limitBody: a body ending exactly at the cap, whose reader reports EOF on a
// separate call, reaches the one-byte probe and ends cleanly with io.EOF.
func TestLimitBody_ExactCapThenSeparateEOF(t *testing.T) {
	b := &limitBody{rc: io.NopCloser(strings.NewReader("abcd")), remaining: 4}
	got, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "abcd" {
		t.Fatalf("got %q", got)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
