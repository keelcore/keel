//go:build !no_remotelog && !windows

// tests/unit/syslog_test.go
package unit

import (
	"net"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// NewSyslogSink returns an error when the TCP endpoint is not reachable.
func TestNewSyslogSink_DialFails(t *testing.T) {
	// Bind then immediately close a listener to obtain a port that is
	// definitely not accepting connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	_, err = logging.NewSyslogSink(addr)
	if err == nil {
		t.Error("expected error dialing closed syslog endpoint, got nil")
	}
}
