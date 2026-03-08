
//go:build !no_statsd

// tests/unit/statsd_gaps_test.go
package unit

import (
	"net"
	"testing"

	"github.com/keelcore/keel/pkg/core/statsd"
)

// New: dial fails on an invalid UDP address.
func TestStatsdNew_DialFails(t *testing.T) {
	_, err := statsd.New("256.256.256.256:99999", "prefix")
	if err == nil {
		t.Error("expected error dialing invalid UDP address, got nil")
	}
}

// Gauge: emits a gauge metric over UDP.
func TestStatsdGauge_Emits(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	c, err := statsd.New(pc.LocalAddr().String(), "svc")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Gauge("heap_bytes", 42.5, map[string]string{"env": "test"})
	// No assertion on content — just verify it does not panic.
}

// tagsStr empty path: nil tags map → no tag suffix emitted.
func TestStatsdGauge_EmptyTags(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	c, err := statsd.New(pc.LocalAddr().String(), "svc")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// nil tags → tagsStr returns "" → no "|#" suffix
	c.Gauge("cpu", 0.5, nil)
}