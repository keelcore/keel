// tests/mockupstream/mock/behaviors.go
// One function per upstream behavior. Every behavior captures the requests it
// reads (headers and body) so tests can assert what keel actually sent.
package mock

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

const modeOK = "ok"

type behavior func(u *upstream, c net.Conn, br *bufio.Reader)

var behaviors = map[string]behavior{
	modeOK:          serveOK,
	"close-idle":    serveCloseIdle,
	"respond-early": serveRespondEarly,
	"no-response":   serveNoResponse,
	"http10":        serveHTTP10,
	"hang":          serveHang,
	"drip":          serveDrip,
	"malformed":     serveMalformed,
	"huge-headers":  serveHugeHeaders,
	"retry-after":   serveRetryAfter,
	"unhealthy":     serveUnhealthy,
}

const (
	okResponse = "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 3\r\n\r\nok\n"
	dripBody   = "drip-drip\n"
	legacyBody = "legacy body, no length, ends at close\n"
	// hugeHeader exceeds net/http's default response-header limit (10 MB).
	hugeHeader = 11 << 20
	// healthPath is the path keel's sidecar health probe polls (sidecar.upstream_health_path).
	healthPath = "/health"
)

func serve(u *upstream, c net.Conn, mode string) {
	defer c.Close()
	behaviors[mode](u, c, bufio.NewReader(c))
}

// readRequest reads one request and captures it whole; false when none arrived.
func readRequest(u *upstream, br *bufio.Reader) (*http.Request, bool) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return nil, false
	}
	dump, err := httputil.DumpRequest(req, true)
	if err != nil {
		return nil, false
	}
	u.capture(dump)
	return req, true
}

// ok: a well-behaved HTTP/1.1 keep-alive server.
func serveOK(u *upstream, c net.Conn, br *bufio.Reader) {
	for {
		req, ok := readRequest(u, br)
		if !ok {
			return
		}
		if _, err := io.WriteString(c, okResponse); err != nil || req.Close {
			return
		}
	}
}

// close-idle: one response, then a silent close with no "Connection: close",
// so the client pools a connection that is already dead.
func serveCloseIdle(u *upstream, c net.Conn, br *bufio.Reader) {
	if _, ok := readRequest(u, br); ok {
		_, _ = io.WriteString(c, okResponse)
	}
}

// respond-early: answers before reading the request (as `nc` does), then
// reads and captures it.
func serveRespondEarly(u *upstream, c net.Conn, br *bufio.Reader) {
	_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nConnection: close\r\n\r\nok\n")
	readRequest(u, br)
}

// no-response: reads the request, then closes without a byte of response.
func serveNoResponse(u *upstream, _ net.Conn, br *bufio.Reader) {
	readRequest(u, br)
}

// http10: an HTTP/1.0 response with no Content-Length; the body ends at close.
func serveHTTP10(u *upstream, c net.Conn, br *bufio.Reader) {
	if _, ok := readRequest(u, br); ok {
		_, _ = io.WriteString(c, "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\n\r\n"+legacyBody)
	}
}

// hang: reads the request and never answers; holds the connection until the
// client gives up.
func serveHang(u *upstream, c net.Conn, br *bufio.Reader) {
	if _, ok := readRequest(u, br); ok {
		_, _ = io.Copy(io.Discard, c)
	}
}

// drip: sends the body one byte every 100 ms.
func serveDrip(u *upstream, c net.Conn, br *bufio.Reader) {
	if _, ok := readRequest(u, br); !ok {
		return
	}
	if _, err := io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 10\r\n\r\n"); err != nil {
		return
	}
	for i := range len(dripBody) {
		time.Sleep(100 * time.Millisecond)
		if _, err := io.WriteString(c, dripBody[i:i+1]); err != nil {
			return
		}
	}
}

// malformed: a status line that is not HTTP.
func serveMalformed(u *upstream, c net.Conn, br *bufio.Reader) {
	if _, ok := readRequest(u, br); ok {
		_, _ = io.WriteString(c, "HTTP/1.1 2OO OK\r\n\r\n")
	}
}

// huge-headers: a response header larger than the client will accept.
func serveHugeHeaders(u *upstream, c net.Conn, br *bufio.Reader) {
	if _, ok := readRequest(u, br); !ok {
		return
	}
	w := bufio.NewWriter(c)
	_, _ = w.WriteString("HTTP/1.1 200 OK\r\nX-Huge: ")
	_, _ = w.WriteString(strings.Repeat("a", hugeHeader))
	_, _ = w.WriteString("\r\nContent-Length: 0\r\n\r\n")
	_ = w.Flush()
}

// retry-after: the first request for each path gets 503 with Retry-After (the
// request's `after` query value, default 1 second); every later one gets 200.
// The health path always gets 200, so keel's probe never consumes the 503.
func serveRetryAfter(u *upstream, c net.Conn, br *bufio.Reader) {
	for {
		req, ok := readRequest(u, br)
		if !ok {
			return
		}
		resp := okResponse
		if req.URL.Path != healthPath && u.firstRequest(req.URL.Path) {
			after := req.URL.Query().Get("after")
			if after == "" {
				after = "1"
			}
			resp = "HTTP/1.1 503 Service Unavailable\r\nRetry-After: " + after + "\r\nContent-Length: 0\r\n\r\n"
		}
		if _, err := io.WriteString(c, resp); err != nil || req.Close {
			return
		}
	}
}

// unhealthy: the health path gets 503 while every other request gets 200.
func serveUnhealthy(u *upstream, c net.Conn, br *bufio.Reader) {
	for {
		req, ok := readRequest(u, br)
		if !ok {
			return
		}
		resp := okResponse
		if req.URL.Path == healthPath {
			resp = "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n"
		}
		if _, err := io.WriteString(c, resp); err != nil || req.Close {
			return
		}
	}
}
