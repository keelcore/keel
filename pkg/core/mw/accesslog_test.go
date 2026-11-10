package mw

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// accessWriter.Unwrap exposes the wrapped writer.
func TestAccessWriter_Unwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	aw := &accessWriter{ResponseWriter: rr, status: http.StatusOK}
	if aw.Unwrap() != rr {
		t.Error("expected Unwrap to return the wrapped ResponseWriter")
	}
}

// clientIP ignores a client-supplied X-Forwarded-For: "ip" is the socket peer.
func TestClientIP_XFFSingleEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.8.7.6:12345"
	req.Header.Set("x-forwarded-for", "1.2.3.4")
	got := clientIP(req)
	if got != "9.8.7.6" {
		t.Errorf("expected socket peer 9.8.7.6, got %q", got)
	}
}

// clientIP returns the remote addr IP when XFF is absent.
func TestClientIP_NoXFF_UsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.8.7.6:12345"
	got := clientIP(req)
	if got != "9.8.7.6" {
		t.Errorf("expected 9.8.7.6, got %q", got)
	}
}

// clientIP returns the raw remote addr when it has no port (not host:port).
func TestClientIP_RemoteAddrNoPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Del("x-forwarded-for")
	req.RemoteAddr = "192.168.1.1" // no port
	got := clientIP(req)
	if got != "192.168.1.1" {
		t.Errorf("expected raw addr 192.168.1.1, got %q", got)
	}
}

func TestAccessLog_WritesLogEntry(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{Out: &buf})
	h := AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo", nil))
	out := buf.String()
	for _, want := range []string{"GET", "/foo", "418"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in log output, got:\n%s", want, out)
		}
	}
}

func TestAccessLog_CapturesBytesOut(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{Out: &buf})
	h := AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	out := buf.String()
	if !strings.Contains(out, "5") {
		t.Errorf("expected byte count 5 in log output, got:\n%s", out)
	}
}

func TestAccessLog_ClientIP_XFF_WithComma(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	h := AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "1.2.3.4, 5.6.7.8")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"ip":"192.0.2.1"`) || !strings.Contains(buf.String(), `"xff":"1.2.3.4, 5.6.7.8"`) {
		t.Errorf("expected socket peer as ip and XFF verbatim as xff, got: %s", buf.String())
	}
}

func TestAccessLog_ClientIP_XFF_WithoutComma(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	h := AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-forwarded-for", "9.8.7.6")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"ip":"192.0.2.1"`) || !strings.Contains(buf.String(), `"xff":"9.8.7.6"`) {
		t.Errorf("expected socket peer as ip and XFF as xff, got: %s", buf.String())
	}
}

// Invalid RemoteAddr makes net.SplitHostPort fail; clientIP returns the raw value.
func TestAccessLog_ClientIP_InvalidRemoteAddr(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{JSON: true, Out: &buf})
	h := AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-a-valid-addr"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"ip":"not-a-valid-addr"`) {
		t.Errorf("expected raw RemoteAddr in log, got: %s", buf.String())
	}
}
