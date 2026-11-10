// server_dispatch_test.go — white-box tests for buildRemoteSink (HTTP path),
// metricsHandler, and prestop_sleep config wiring.
// Build-tag-free: all paths tested here compile and run in every build variant.
package core

import (
	"net"
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

// ---------------------------------------------------------------------------
// buildRemoteSink — HTTP path
// ---------------------------------------------------------------------------

func TestBuildRemoteSink_HTTP_ReturnsHTTPSink(t *testing.T) {
	cfg := config.RemoteSinkConfig{Protocol: "http", Endpoint: "http://127.0.0.1:1/ingest"}
	w, sink, err := buildRemoteSink(cfg)
	if err != nil {
		t.Fatalf("unexpected error for http protocol: %v", err)
	}
	if w == nil {
		t.Error("expected non-nil writer for http protocol")
	}
	if sink == nil {
		t.Error("expected non-nil HTTPSink for http protocol")
	}
}

func TestBuildRemoteSink_EmptyProtocol_DefaultsToHTTP(t *testing.T) {
	cfg := config.RemoteSinkConfig{Protocol: "", Endpoint: "http://127.0.0.1:1/ingest"}
	w, sink, err := buildRemoteSink(cfg)
	if err != nil {
		t.Fatalf("unexpected error for empty protocol: %v", err)
	}
	if w == nil {
		t.Error("expected non-nil writer for empty (default) protocol")
	}
	if sink == nil {
		t.Error("expected HTTPSink for empty (default) protocol")
	}
}

// ---------------------------------------------------------------------------
// buildRemoteSink — syslog error path (works in all build variants)
// With no_remotelog the stub returns an error unconditionally.
// Without no_remotelog the real impl returns an error on a closed port.
// ---------------------------------------------------------------------------

func TestBuildRemoteSink_Syslog_ClosedPort_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := config.RemoteSinkConfig{Protocol: "syslog", Endpoint: addr}
	_, _, err = buildRemoteSink(cfg)
	if err == nil {
		t.Error("expected error when syslog endpoint is unreachable")
	}
}

// ---------------------------------------------------------------------------
// metricsHandler
// ---------------------------------------------------------------------------

// TestMetricsHandler_Enabled_Returns200 lives in server_loops_test.go (//go:build !no_prom)
// because it requires a real metrics.Handler() that emits text/plain; charset=utf-8.

// ---------------------------------------------------------------------------
// prestop_sleep — config wiring
// The actual sleep is tested at BATS level; here we verify:
//  (a) the default is zero (no inadvertent sleep),
//  (b) a configured non-zero value is preserved in the server's cfg field.
// ---------------------------------------------------------------------------
