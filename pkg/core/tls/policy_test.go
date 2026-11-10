package tls

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/tests/unit"
)

func TestBuildTLSConfig_MinVersionTLS13(t *testing.T) {
	cfg := BuildTLSConfig(config.Config{})
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = 0x%04x, want 0x%04x (TLS 1.3)", cfg.MinVersion, tls.VersionTLS13)
	}
}

func TestBuildTLSConfig_NoMaxVersionCeiling(t *testing.T) {
	cfg := BuildTLSConfig(config.Config{})
	if cfg.MaxVersion != 0 {
		t.Errorf("MaxVersion = 0x%04x, want 0 (no ceiling)", cfg.MaxVersion)
	}
}

func TestTLS12ConnectionRejected(t *testing.T) {
	certPEM, keyPEM := unit.TLSTestCert(t, time.Now().Add(time.Hour))

	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	serverCfg := BuildTLSConfig(config.Config{})
	serverCfg.Certificates = []tls.Certificate{serverCert}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Accept connections in background; ignore errors (handshake will fail).
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.(*tls.Conn).Handshake()
			}(conn)
		}
	}()

	clientCfg := &tls.Config{
		InsecureSkipVerify: true, // test only
		MaxVersion:         tls.VersionTLS12,
	}
	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err == nil {
		conn.Close()
		t.Fatal("expected TLS 1.2 connection to be rejected, but handshake succeeded")
	}
}

func TestBuildTLSConfigFIPS(t *testing.T) {
	cfg := BuildTLSConfig(config.Config{})
	if cfg == nil {
		t.Fatal("BuildTLSConfig returned nil")
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %d, want %d", cfg.MinVersion, tls.VersionTLS13)
	}
	want := []tls.CurveID{tls.CurveP256, tls.CurveP384}
	if len(cfg.CurvePreferences) != len(want) {
		t.Fatalf("CurvePreferences = %v, want %v", cfg.CurvePreferences, want)
	}
	for i, c := range want {
		if cfg.CurvePreferences[i] != c {
			t.Fatalf("CurvePreferences[%d] = %v, want %v", i, cfg.CurvePreferences[i], c)
		}
	}
}
