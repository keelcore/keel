//go:build !no_remotelog && !windows

// server_dispatch_syslog_test.go — white-box test for buildRemoteSink syslog
// success path. Requires the real syslog implementation (not the stub).
package core

import (
	"net"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

func TestBuildRemoteSink_Syslog_Success_ReturnsWriterNoHTTPSink(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	cfg := config.RemoteSinkConfig{Protocol: "syslog", Endpoint: ln.Addr().String()}
	w, sink, err := buildRemoteSink(cfg)
	if err != nil {
		t.Fatalf("buildRemoteSink syslog: unexpected error: %v", err)
	}
	if w == nil {
		t.Error("expected non-nil writer for syslog protocol")
	}
	if sink != nil {
		t.Errorf("expected nil HTTPSink for syslog protocol, got %T", sink)
	}
}
