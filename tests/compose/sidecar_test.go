// tests/compose/sidecar_test.go
// Sidecar resilience: keel-sidecar proxies to mockupstream, which misbehaves on
// demand the way legacy services do. Each test switches the mock's mode (which
// drops its open connections), sends traffic through keel-sidecar, and checks
// both what the client got and what the upstream received (the mock's captures).
// The tests share the mock and run in file order.
package compose

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

const legacyBody = "legacy body, no length, ends at close\n"

// sidecarReady puts the mock back in ok mode and waits until keel-sidecar's
// health probe sees it, so each test starts from a healthy upstream.
func sidecarReady(t *testing.T) {
	t.Helper()
	skip(t)
	waitHTTP(t, keelURL(MockControl, "/captures"), http.StatusOK, 60*time.Second)
	mockMode(t, "ok")
	waitHTTP(t, keelURL(SidecarReady, "/readyz"), http.StatusOK, 60*time.Second)
}

// do sends a control or scrape request (mock control plane, sidecar admin). It
// uses its own context: t.Context() is already canceled when t.Cleanup runs.
func do(t *testing.T, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func mockPost(t *testing.T, path string) {
	t.Helper()
	resp := do(t, http.MethodPost, keelURL(MockControl, path))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("mock %s: status %d", path, resp.StatusCode)
	}
}

func mockMode(t *testing.T, mode string) {
	t.Helper()
	mockPost(t, "/mode/"+mode)
}

func mockCaptures(t *testing.T) string {
	t.Helper()
	resp := do(t, http.MethodGet, keelURL(MockControl, "/captures"))
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("mock captures: %v", err)
	}
	return string(b)
}

type proxied struct {
	status  int
	body    string
	elapsed time.Duration
}

// send sends one request through keel-sidecar on a fresh connection.
func send(method, path, body string, hdr map[string]string) (proxied, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, keelURL(SidecarHTTP, path), strings.NewReader(body))
	if err != nil {
		return proxied{}, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return proxied{}, fmt.Errorf("%s %s via sidecar: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return proxied{}, fmt.Errorf("%s %s via sidecar: read body: %w", method, path, err)
	}
	return proxied{status: resp.StatusCode, body: string(b), elapsed: time.Since(start)}, nil
}

func viaSidecar(t *testing.T, method, path, body string, hdr map[string]string) proxied {
	t.Helper()
	got, err := send(method, path, body, hdr)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// sidecarMetric reads one unlabelled counter from keel-sidecar's /metrics.
func sidecarMetric(t *testing.T, name string) int64 {
	t.Helper()
	resp := do(t, http.MethodGet, keelURL(SidecarAdmin, "/metrics"))
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), name+" "); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				t.Fatalf("metric %s: %v", name, err)
			}
			return n
		}
	}
	t.Fatalf("metric %s not exposed", name)
	return 0
}

func requests(captures, requestLine string) int {
	return strings.Count(captures, requestLine)
}

// keel replaces the client's credentials with its own signed JWT.
func TestCompose_SidecarOutboundSigning(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "ok")
	got := viaSidecar(t, http.MethodGet, "/signed", "", map[string]string{"Authorization": "Bearer forged-client-token"})
	if got.status != http.StatusOK || got.body != "ok\n" {
		t.Fatalf("status %d body %q, want 200 ok", got.status, got.body)
	}
	c := mockCaptures(t)
	if requests(c, "GET /signed ") != 1 || !strings.Contains(c, "Authorization: Bearer ey") {
		t.Fatalf("upstream did not receive keel's signed JWT:\n%s", c)
	}
	if strings.Contains(c, "forged-client-token") {
		t.Fatalf("client credentials reached the upstream:\n%s", c)
	}
}

// Upstream briefly refusing connections (restart): keel retries the dial and
// the request — a POST — is delivered exactly once.
func TestCompose_SidecarDialRetryRecovers(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "ok")
	mockPost(t, "/down")
	t.Cleanup(func() { mockPost(t, "/up") })
	before := sidecarMetric(t, "keel_upstream_dial_retries_total")

	type result struct {
		got proxied
		err error
	}
	done := make(chan result, 1)
	go func() {
		got, err := send(http.MethodPost, "/retry", "payload", nil)
		done <- result{got, err}
	}()

	// The retry counter moving proves keel is between attempts; bring the
	// upstream back before its next one.
	deadline := time.Now().Add(5 * time.Second)
	for sidecarMetric(t, "keel_upstream_dial_retries_total") == before {
		if time.Now().After(deadline) {
			t.Fatal("keel never retried the upstream dial")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mockPost(t, "/up")

	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	got := r.got
	if got.status != http.StatusOK {
		t.Fatalf("status %d body %q, want 200 after the upstream came back", got.status, got.body)
	}
	if n := requests(mockCaptures(t), "POST /retry "); n != 1 {
		t.Fatalf("upstream received POST /retry %d times, want 1", n)
	}
}

// Upstream down for longer than the retry window: 502, counted once.
func TestCompose_SidecarDialFailureIs502(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "ok")
	mockPost(t, "/down")
	t.Cleanup(func() { mockPost(t, "/up") })
	before := sidecarMetric(t, "keel_upstream_dial_failures_total")
	got := viaSidecar(t, http.MethodPost, "/dead", "payload", nil)
	if got.status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", got.status)
	}
	if after := sidecarMetric(t, "keel_upstream_dial_failures_total"); after != before+1 {
		t.Fatalf("dial failures %d → %d, want +1", before, after)
	}
}

// Upstream reads a POST and closes without answering: 502, and the request is
// not replayed (it may have been acted on).
func TestCompose_SidecarNoResponseNotReplayed(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "no-response")
	got := viaSidecar(t, http.MethodPost, "/once", "payload", nil)
	if got.status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", got.status)
	}
	if n := requests(mockCaptures(t), "POST /once "); n != 1 {
		t.Fatalf("upstream received POST /once %d times, want 1", n)
	}
}

// Upstream answers before reading the request (as nc does).
func TestCompose_SidecarRespondEarly(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "respond-early")
	if got := viaSidecar(t, http.MethodGet, "/early", "", nil); got.status != http.StatusOK || got.body != "ok\n" {
		t.Fatalf("status %d body %q, want 200 ok", got.status, got.body)
	}
}

// HTTP/1.0 response with no Content-Length: the body ends at close.
func TestCompose_SidecarHTTP10(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "http10")
	if got := viaSidecar(t, http.MethodGet, "/legacy", "", nil); got.status != http.StatusOK || got.body != legacyBody {
		t.Fatalf("status %d body %q, want 200 %q", got.status, got.body, legacyBody)
	}
}

// Upstream closes every connection after one response without saying so:
// keel's pooled connection is dead on reuse, and every request still succeeds.
func TestCompose_SidecarCloseIdle(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "close-idle")
	for i := range 5 {
		path := fmt.Sprintf("/idle-%d", i)
		if got := viaSidecar(t, http.MethodGet, path, "", nil); got.status != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", path, got.status)
		}
	}
}

// Hung upstream: 502 once timeouts.write (3s) nears, never a hung client.
func TestCompose_SidecarHangIs502(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "hang")
	got := viaSidecar(t, http.MethodGet, "/hang", "", nil)
	if got.status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", got.status)
	}
	if got.elapsed < 2*time.Second || got.elapsed > 6*time.Second {
		t.Fatalf("502 after %s, want ~2.7s (90%% of timeouts.write)", got.elapsed)
	}
}

// Slow body: streamed through in full.
func TestCompose_SidecarDrip(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "drip")
	if got := viaSidecar(t, http.MethodGet, "/drip", "", nil); got.status != http.StatusOK || got.body != "drip-drip\n" {
		t.Fatalf("status %d body %q, want 200 drip-drip", got.status, got.body)
	}
}

// Status line that is not HTTP: 502.
func TestCompose_SidecarMalformedIs502(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "malformed")
	if got := viaSidecar(t, http.MethodGet, "/bad", "", nil); got.status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", got.status)
	}
}

// Response header past net/http's limit: 502, and none of it reaches the client.
func TestCompose_SidecarHugeHeadersIs502(t *testing.T) {
	sidecarReady(t)
	mockMode(t, "huge-headers")
	got := viaSidecar(t, http.MethodGet, "/huge", "", nil)
	if got.status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", got.status)
	}
	if strings.Contains(got.body, "aaaa") {
		t.Fatal("upstream header bytes leaked to the client")
	}
}
