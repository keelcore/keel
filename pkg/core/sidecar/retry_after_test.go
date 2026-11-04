//go:build !no_sidecar

package sidecar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

// scriptedRT answers each round trip with the next response in its script and
// records every request it was sent.
type scriptedRT struct {
	script []*http.Response
	sent   []*http.Request
	closed atomic.Int32
}

func (s *scriptedRT) RoundTrip(req *http.Request) (*http.Response, error) {
	s.sent = append(s.sent, req)
	resp := s.script[len(s.sent)-1]
	resp.Body = &trackBody{ReadCloser: resp.Body, closed: &s.closed}
	return resp, nil
}

type trackBody struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (b *trackBody) Close() error {
	b.closed.Add(1)
	return b.ReadCloser.Close()
}

func reply(code int, retryAfter string) *http.Response {
	h := http.Header{}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader("body"))}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"0", 0, true},
		{"2", 2 * time.Second, true},
		{" 7 ", 7 * time.Second, true},
		{now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second, true},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0, true},
		{"", 0, false},
		{"-1", 0, false},
		{"soon", 0, false},
		{"1.5", 0, false},
	} {
		got, ok := parseRetryAfter(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = %s, %v; want %s, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReplayable(t *testing.T) {
	withBody := func(method string, getBody bool) *http.Request {
		r, err := http.NewRequestWithContext(context.Background(), method, "http://u/", strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}
		if !getBody {
			r.GetBody = nil
		}
		return r
	}
	for _, tc := range []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"GET without body", httptest.NewRequest(http.MethodGet, "http://u/", nil), true},
		{"HEAD", httptest.NewRequest(http.MethodHead, "http://u/", nil), true},
		{"DELETE", httptest.NewRequest(http.MethodDelete, "http://u/", nil), true},
		{"PUT with a replayable body", withBody(http.MethodPut, true), true},
		{"PUT with a one-shot body", withBody(http.MethodPut, false), false},
		{"POST", httptest.NewRequest(http.MethodPost, "http://u/", nil), false},
		{"PATCH", httptest.NewRequest(http.MethodPatch, "http://u/", nil), false},
	} {
		if got := replayable(tc.req); got != tc.want {
			t.Errorf("%s: replayable = %v, want %v", tc.name, got, tc.want)
		}
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

func TestRetryAfter_ReplaysTheBody(t *testing.T) {
	rt := &scriptedRT{script: []*http.Response{reply(http.StatusServiceUnavailable, "0"), reply(http.StatusOK, "")}}
	tr := &retryAfterTransport{inner: rt, budget: time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, "http://u/x", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	b, err := io.ReadAll(rt.sent[1].Body)
	if err != nil || string(b) != "payload" {
		t.Fatalf("retried body = %q, %v; want payload", b, err)
	}
}

func TestRetryAfter_PassesThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *http.Request
		resp *http.Response
	}{
		{"POST is never replayed", httptest.NewRequest(http.MethodPost, "http://u/", nil), reply(http.StatusServiceUnavailable, "0")},
		{"no Retry-After", httptest.NewRequest(http.MethodGet, "http://u/", nil), reply(http.StatusServiceUnavailable, "")},
		{"invalid Retry-After", httptest.NewRequest(http.MethodGet, "http://u/", nil), reply(http.StatusServiceUnavailable, "soon")},
		{"delay past the deadline", httptest.NewRequest(http.MethodGet, "http://u/", nil), reply(http.StatusServiceUnavailable, "5")},
		{"other status", httptest.NewRequest(http.MethodGet, "http://u/", nil), reply(http.StatusBadGateway, "0")},
	} {
		rt := &scriptedRT{script: []*http.Response{tc.resp}}
		tr := &retryAfterTransport{inner: rt, budget: time.Second}
		resp, err := tr.RoundTrip(tc.req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.resp.StatusCode || len(rt.sent) != 1 {
			t.Fatalf("%s: status %d, sent %d; want %d passed through, 1 sent", tc.name, resp.StatusCode, len(rt.sent), tc.resp.StatusCode)
		}
	}
}

func TestRetryAfter_ClientGoneDuringTheDelay(t *testing.T) {
	rt := &scriptedRT{script: []*http.Response{reply(http.StatusServiceUnavailable, "1")}}
	tr := &retryAfterTransport{inner: rt, budget: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	req := httptest.NewRequest(http.MethodGet, "http://u/", nil).WithContext(ctx)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("want the client's cancellation, got a response")
	}
	if len(rt.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(rt.sent))
	}
}

// Through the proxy: honor_retry_after on retries a 503 after its delay; off passes it through.
func TestNewWithHooks_HonorRetryAfter(t *testing.T) {
	for _, honor := range []bool{true, false} {
		var calls atomic.Int32
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, "ok")
		}))
		cfg := config.Config{
			Timeouts: config.TimeoutsConfig{Write: config.DurationOf(5 * time.Second)},
			Sidecar: config.SidecarConfig{
				UpstreamURL: up.URL,
				Retry:       config.SidecarRetryConfig{DialAttempts: 1, HonorRetryAfter: honor},
			},
		}
		h, err := NewWithHooks(cfg, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://sidecar.local/", nil))
		up.Close()
		want, wantCalls := http.StatusOK, int32(2)
		if !honor {
			want, wantCalls = http.StatusServiceUnavailable, 1
		}
		if rec.Code != want || calls.Load() != wantCalls {
			t.Fatalf("honor=%v: status %d after %d upstream calls; want %d after %d", honor, rec.Code, calls.Load(), want, wantCalls)
		}
	}
}
