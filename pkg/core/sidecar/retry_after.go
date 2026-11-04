//go:build !no_sidecar

package sidecar

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfterTransport honors an upstream's Retry-After (sidecar.retry.honor_retry_after):
// a 503 or 429 that carries it is retried once, after the stated delay, when the
// request can be sent again unchanged and the delay plus the retry fit inside
// budget (the upstream response deadline). Otherwise the response passes through.
// It sits inside the circuit breaker, so the breaker sees one outcome per request.
type retryAfterTransport struct {
	inner  http.RoundTripper
	budget time.Duration
	hooks  Hooks
}

func (t *retryAfterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.inner.RoundTrip(req)
	if err != nil || !retryableStatus(resp.StatusCode) || !replayable(req) {
		return resp, err
	}
	delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), start)
	if !ok || time.Since(start)+delay >= t.budget {
		return resp, nil
	}
	ctx, cancel := context.WithDeadline(req.Context(), start.Add(t.budget))
	retry, err := rewind(ctx, req)
	if err != nil {
		cancel()
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	t.hooks.warn("upstream_retry_after", map[string]any{"status": resp.StatusCode, "delay": delay.String()})
	call(t.hooks.RetryAfter)

	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		cancel()
		return nil, ctx.Err()
	case <-timer.C:
	}
	resp, err = t.inner.RoundTrip(retry)
	if err != nil {
		cancel()
		return nil, err
	}
	// The deadline bounds the retry's body too; it ends when the proxy closes it.
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func retryableStatus(code int) bool {
	return code == http.StatusServiceUnavailable || code == http.StatusTooManyRequests
}

// replayable: an idempotent method whose body (if any) can be produced again, so a
// second copy of the request has the same effect as the first.
func replayable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// rewind is a copy of req, bound to ctx, with a fresh body.
func rewind(ctx context.Context, req *http.Request) (*http.Request, error) {
	out := req.Clone(ctx)
	if req.Body == nil || req.Body == http.NoBody {
		return out, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

// parseRetryAfter reads Retry-After (RFC 9110 §10.2.3): delay-seconds or an
// HTTP-date, the latter relative to now; a date in the past is no delay.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseUint(v, 10, 32); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	at, err := http.ParseTime(v)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
