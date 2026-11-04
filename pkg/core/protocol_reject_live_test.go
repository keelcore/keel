package core

import (
	"context"
	cryptotls "crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/protonotice"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// U1 wiring: the HTTPS listener reports a refused handshake to the runner's observer.
func TestServeHTTPS_ReportsRefusedHandshakes(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	addrCh := make(chan string, 1)
	orig := netListen
	netListen = func(network, _ string) (net.Listener, error) {
		ln, err := orig(network, "127.0.0.1:0")
		if err == nil {
			addrCh <- ln.Addr().String()
		}
		return ln, err
	}
	t.Cleanup(func() { netListen = orig })

	var mu sync.Mutex
	var seen []protonotice.Code
	r := realRunner{onReject: func(c protonotice.Code, _ *cryptotls.ClientHelloInfo) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
	}}
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.serveHTTPS(ctx, lifecycle.NewShutdownOrchestrator(log), "127.0.0.1:0", http.NotFoundHandler(), shortDrainCfg(), loader, nil, log)
	}()
	addr := <-addrCh

	conn, err := cryptotls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr,
		&cryptotls.Config{MaxVersion: cryptotls.VersionTLS12, ServerName: "test"})
	if err == nil {
		_ = conn.Close()
		t.Fatal("TLS 1.2 handshake succeeded, want it refused")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != protonotice.TLSVersion {
		t.Fatalf("observed %v, want [%s]", seen, protonotice.TLSVersion)
	}
}
