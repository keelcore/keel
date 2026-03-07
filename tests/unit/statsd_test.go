//go:build !no_statsd

package unit

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/statsd"
)

func TestStatsD_EmitsRequestsTotal(t *testing.T) {
	// Bind a UDP listener on an OS-assigned port.
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	client, err := statsd.New(ln.LocalAddr().String(), "keel")
	if err != nil {
		t.Fatal(err)
	}

	h := statsd.Instrument(client, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	// Read the datagram(s) emitted during the request.
	buf := make([]byte, 4096)
	_ = ln.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var received []string
	for {
		n, _, err := ln.ReadFrom(buf)
		if err != nil {
			break
		}
		received = append(received, string(buf[:n]))
	}

	combined := strings.Join(received, "\n")
	if !strings.Contains(combined, "requests_total") {
		t.Errorf("expected requests_total in StatsD output, got: %q", combined)
	}
	if !strings.Contains(combined, "method:GET") {
		t.Errorf("expected method:GET tag in StatsD output, got: %q", combined)
	}
}

func TestStatsD_EmitsRequestDuration(t *testing.T) {
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	client, err := statsd.New(ln.LocalAddr().String(), "keel")
	if err != nil {
		t.Fatal(err)
	}

	h := statsd.Instrument(client, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	buf := make([]byte, 4096)
	_ = ln.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var received []string
	for {
		n, _, err := ln.ReadFrom(buf)
		if err != nil {
			break
		}
		received = append(received, string(buf[:n]))
	}

	combined := strings.Join(received, "\n")
	if !strings.Contains(combined, "request_duration_ms") {
		t.Errorf("expected request_duration_ms in StatsD output, got: %q", combined)
	}
}