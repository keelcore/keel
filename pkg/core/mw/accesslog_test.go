package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
