//go:build !no_remotelog && !windows

// tests/unit/syslog_test.go
package unit

import (
	"net"
	"testing"
	"time"

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

// NewSyslogSink succeeds and sends data when the endpoint is listening.
func TestNewSyslogSink_DialSuccess_DeliversSyslogData(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	w, err := logging.NewSyslogSink(ln.Addr().String())
	if err != nil {
		t.Fatalf("NewSyslogSink: unexpected error: %v", err)
	}

	// Accept the connection in the background.
	connCh := make(chan net.Conn, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr == nil {
			connCh <- c
		}
	}()

	// Write through the sink; syslog wraps in RFC 5424 framing.
	if _, werr := w.Write([]byte("hello syslog")); werr != nil {
		t.Fatalf("Write: %v", werr)
	}

	select {
	case conn := <-connCh:
		defer conn.Close()
		buf := make([]byte, 512)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, rerr := conn.Read(buf)
		if rerr != nil {
			t.Fatalf("read from syslog connection: %v", rerr)
		}
		if n == 0 {
			t.Error("expected data from syslog sink, got zero bytes")
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for syslog connection")
	}
}
