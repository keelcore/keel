// tests/mockupstream/mock/behaviors_test.go
// Unit tests for behaviors.go: each behavior serves one end of a net.Pipe and
// the test plays keel's side on the other.
package mock

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// served starts serve in mode on one end of a pipe and returns the upstream,
// the client end (with a deadline) and a channel closed when serve returns.
func served(t *testing.T, mode string) (*upstream, net.Conn, <-chan struct{}) {
	t.Helper()
	u := newTestUpstream()
	server, client := net.Pipe()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	done := make(chan struct{})
	go func() { serve(u, server, mode); close(done) }()
	t.Cleanup(func() { _ = client.Close(); <-done })
	return u, client, done
}

func send(t *testing.T, c net.Conn, raw string) {
	t.Helper()
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

// readResponse reads one response and its whole body from br.
func readResponse(t *testing.T, br *bufio.Reader) (*http.Response, string) {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

func get(path string) string { return "GET " + path + " HTTP/1.1\r\nHost: up\r\n\r\n" }

func getClose(path string) string {
	return "GET " + path + " HTTP/1.1\r\nHost: up\r\nConnection: close\r\n\r\n"
}

// requests counts the requests captured for "GET path ".
func requests(u *upstream, path string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return strings.Count(u.captures.String(), "GET "+path+" ")
}

func TestBehaviors_EveryModeReturnsWhenNoRequestArrives(t *testing.T) {
	for mode := range behaviors {
		t.Run(mode, func(t *testing.T) {
			u, client, done := served(t, mode)
			if mode == "respond-early" {
				readResponse(t, bufio.NewReader(client))
			}
			_ = client.Close()
			waitClosed(t, done, "serve to return")
			if u.captures.Len() != 0 {
				t.Fatalf("captured %q from no request", u.captures.String())
			}
		})
	}
}

func TestReadRequest_DropsARequestWhoseBodyIsCutShort(t *testing.T) {
	u, client, done := served(t, modeOK)
	send(t, client, "POST / HTTP/1.1\r\nHost: up\r\nContent-Length: 10\r\n\r\nabc")
	_ = client.Close()
	waitClosed(t, done, "serve to return")
	if u.captures.Len() != 0 {
		t.Fatalf("captured a truncated request: %q", u.captures.String())
	}
}

func TestServeOK_KeepsTheConnectionAliveUntilConnectionClose(t *testing.T) {
	u, client, done := served(t, modeOK)
	br := bufio.NewReader(client)
	for _, raw := range []string{get("/a"), get("/b"), getClose("/c")} {
		send(t, client, raw)
		if resp, body := readResponse(t, br); resp.StatusCode != http.StatusOK || body != "ok\n" {
			t.Fatalf("status %d body %q, want 200 ok", resp.StatusCode, body)
		}
	}
	waitClosed(t, done, "serve to return after Connection: close")
	if requests(u, "/a") != 1 || requests(u, "/b") != 1 || requests(u, "/c") != 1 {
		t.Fatalf("captures:\n%s", u.captures.String())
	}
}

func TestBehaviors_ReturnWhenTheClientLeavesBeforeTheResponse(t *testing.T) {
	for _, mode := range []string{modeOK, "drip", "retry-after", "unhealthy"} {
		t.Run(mode, func(t *testing.T) {
			u, client, done := served(t, mode)
			send(t, client, get("/x"))
			_ = client.Close()
			waitClosed(t, done, "serve to return")
			if requests(u, "/x") != 1 {
				t.Fatalf("captures:\n%s", u.captures.String())
			}
		})
	}
}

func TestServeCloseIdle_AnswersOnceThenClosesSilently(t *testing.T) {
	u, client, done := served(t, "close-idle")
	br := bufio.NewReader(client)
	send(t, client, get("/a"))
	resp, body := readResponse(t, br)
	if resp.StatusCode != http.StatusOK || body != "ok\n" || resp.Close {
		t.Fatalf("status %d body %q close %v, want 200 ok with keep-alive", resp.StatusCode, body, resp.Close)
	}
	waitClosed(t, done, "serve to close the connection")
	if requests(u, "/a") != 1 {
		t.Fatalf("captures:\n%s", u.captures.String())
	}
}

func TestServeRespondEarly_AnswersBeforeReadingTheRequest(t *testing.T) {
	u, client, done := served(t, "respond-early")
	br := bufio.NewReader(client)
	resp, body := readResponse(t, br)
	if resp.StatusCode != http.StatusOK || body != "ok\n" {
		t.Fatalf("status %d body %q, want 200 ok", resp.StatusCode, body)
	}
	send(t, client, get("/late"))
	waitClosed(t, done, "serve to return")
	if requests(u, "/late") != 1 {
		t.Fatalf("the request sent after the response was not captured:\n%s", u.captures.String())
	}
}

func TestServeNoResponse_ReadsTheRequestAndClosesWithoutAByte(t *testing.T) {
	u, client, done := served(t, "no-response")
	send(t, client, get("/a"))
	got, err := io.ReadAll(client)
	if err != nil || len(got) != 0 {
		t.Fatalf("read %q (err %v), want EOF with no bytes", got, err)
	}
	waitClosed(t, done, "serve to return")
	if requests(u, "/a") != 1 {
		t.Fatalf("captures:\n%s", u.captures.String())
	}
}

func TestServeHTTP10_EndsTheBodyAtClose(t *testing.T) {
	_, client, _ := served(t, "http10")
	send(t, client, get("/a"))
	resp, body := readResponse(t, bufio.NewReader(client))
	if resp.ProtoMajor != 1 || resp.ProtoMinor != 0 || resp.ContentLength != -1 || body != legacyBody {
		t.Fatalf("proto %s length %d body %q, want HTTP/1.0, no length, %q",
			resp.Proto, resp.ContentLength, body, legacyBody)
	}
}

func TestServeHang_HoldsTheConnectionUntilTheClientGivesUp(t *testing.T) {
	u, client, done := served(t, "hang")
	send(t, client, get("/a"))
	select {
	case <-done:
		t.Fatal("hang returned while the client was still connected")
	case <-time.After(50 * time.Millisecond):
	}
	_ = client.Close()
	waitClosed(t, done, "serve to return once the client left")
	if requests(u, "/a") != 1 {
		t.Fatalf("captures:\n%s", u.captures.String())
	}
}

func TestServeDrip_SendsTheBodyOneByteAtATime(t *testing.T) {
	_, client, _ := served(t, "drip")
	send(t, client, get("/a"))
	start := time.Now()
	resp, body := readResponse(t, bufio.NewReader(client))
	if resp.StatusCode != http.StatusOK || body != dripBody {
		t.Fatalf("status %d body %q, want 200 %q", resp.StatusCode, body, dripBody)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("body arrived in %s, want about a second", elapsed)
	}
}

func TestServeDrip_StopsWhenTheClientLeavesMidBody(t *testing.T) {
	_, client, done := served(t, "drip")
	send(t, client, get("/a"))
	if _, err := http.ReadResponse(bufio.NewReader(client), nil); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	waitClosed(t, done, "serve to stop dripping")
}

func TestServeMalformed_SendsAStatusLineThatIsNotHTTP(t *testing.T) {
	_, client, _ := served(t, "malformed")
	send(t, client, get("/a"))
	if _, err := http.ReadResponse(bufio.NewReader(client), nil); err == nil {
		t.Fatal("the malformed status line parsed")
	}
}

func TestServeHugeHeaders_SendsAHeaderPastTheClientLimit(t *testing.T) {
	_, client, done := served(t, "huge-headers")
	send(t, client, get("/a"))
	n, err := io.Copy(io.Discard, client)
	if err != nil {
		t.Fatal(err)
	}
	if n <= hugeHeader {
		t.Fatalf("sent %d bytes, want more than %d", n, hugeHeader)
	}
	waitClosed(t, done, "serve to return")
}

func TestServeRetryAfter_503sEachPathOnceExceptHealth(t *testing.T) {
	u, client, done := served(t, "retry-after")
	br := bufio.NewReader(client)
	for _, step := range []struct {
		raw, retryAfter string
		status          int
	}{
		{get(healthPath), "", http.StatusOK},
		{get(healthPath), "", http.StatusOK},
		{get("/a?after=7"), "7", http.StatusServiceUnavailable},
		{get("/a?after=7"), "", http.StatusOK},
		{get("/b"), "1", http.StatusServiceUnavailable},
		{getClose("/b"), "", http.StatusOK},
	} {
		send(t, client, step.raw)
		resp, _ := readResponse(t, br)
		if resp.StatusCode != step.status || resp.Header.Get("Retry-After") != step.retryAfter {
			t.Fatalf("%q: status %d Retry-After %q, want %d %q",
				step.raw, resp.StatusCode, resp.Header.Get("Retry-After"), step.status, step.retryAfter)
		}
	}
	waitClosed(t, done, "serve to return after Connection: close")
	if requests(u, "/a?after=7") != 2 || requests(u, "/b") != 2 {
		t.Fatalf("captures:\n%s", u.captures.String())
	}
}

func TestServeUnhealthy_503sOnlyTheHealthPath(t *testing.T) {
	_, client, done := served(t, "unhealthy")
	br := bufio.NewReader(client)
	for _, step := range []struct {
		raw    string
		status int
	}{
		{get(healthPath), http.StatusServiceUnavailable},
		{get("/a"), http.StatusOK},
		{getClose(healthPath), http.StatusServiceUnavailable},
	} {
		send(t, client, step.raw)
		if resp, _ := readResponse(t, br); resp.StatusCode != step.status {
			t.Fatalf("%q: status %d, want %d", step.raw, resp.StatusCode, step.status)
		}
	}
	waitClosed(t, done, "serve to return after Connection: close")
}
