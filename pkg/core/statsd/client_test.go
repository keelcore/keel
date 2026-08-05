//go:build !no_statsd

package statsd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newLoopbackClient returns a Client wired to a UDP listener the test fully
// owns, plus the listener so the test can read what was emitted. No external
// StatsD daemon or public port is involved.
func newLoopbackClient(t *testing.T, prefix string) (*Client, *net.UDPConn) {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	c, err := New(pc.LocalAddr().String(), prefix)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.conn.Close() })
	return c, pc
}

func readOne(t *testing.T, pc *net.UDPConn) string {
	t.Helper()
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := pc.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	return string(buf[:n])
}

func TestNewError(t *testing.T) {
	// Missing port is a purely syntactic failure — no DNS, no network.
	if _, err := New("127.0.0.1", "keel"); err == nil {
		t.Fatal("expected error for endpoint missing port")
	}
}

func TestCount(t *testing.T) {
	c, pc := newLoopbackClient(t, "keel")
	c.Count("requests_total", 3, map[string]string{"method": "GET"})
	if got := readOne(t, pc); got != "keel.requests_total:3|c|#method:GET" {
		t.Fatalf("unexpected payload %q", got)
	}
}

func TestTiming(t *testing.T) {
	c, pc := newLoopbackClient(t, "keel")
	c.Timing("request_duration_ms", 12, nil)
	if got := readOne(t, pc); got != "keel.request_duration_ms:12|ms" {
		t.Fatalf("unexpected payload %q", got)
	}
}

func TestGauge(t *testing.T) {
	c, pc := newLoopbackClient(t, "keel")
	c.Gauge("inflight", 1.5, map[string]string{"z": "1", "a": "2"})
	// Tags must be sorted for determinism.
	if got := readOne(t, pc); got != "keel.inflight:1.5|g|#a:2,z:1" {
		t.Fatalf("unexpected payload %q", got)
	}
}

func TestTagsStrEmpty(t *testing.T) {
	if s := tagsStr(nil); s != "" {
		t.Fatalf("want empty, got %q", s)
	}
	if s := tagsStr(map[string]string{}); s != "" {
		t.Fatalf("want empty, got %q", s)
	}
}

func TestStatusCaptureUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sc := &statusCapture{ResponseWriter: rec, status: http.StatusOK}
	if sc.Unwrap() != rec {
		t.Fatal("Unwrap did not return wrapped writer")
	}
}

func TestInstrument(t *testing.T) {
	c, pc := newLoopbackClient(t, "keel")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated) // exercises statusCapture.WriteHeader
	})
	h := Instrument(c, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status not propagated, got %d", rec.Code)
	}
	// Instrument emits Count then Timing; assert both land on the socket.
	first := readOne(t, pc)
	second := readOne(t, pc)
	joined := first + "\n" + second
	if !strings.Contains(joined, "keel.requests_total:1|c|#method:PUT,status:201") {
		t.Fatalf("count metric missing: %q", joined)
	}
	if !strings.Contains(joined, "keel.request_duration_ms:") {
		t.Fatalf("timing metric missing: %q", joined)
	}
}
