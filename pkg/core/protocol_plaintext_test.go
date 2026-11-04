package core

import (
	"context"
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

// U6: plain HTTP sent to the HTTPS port gets the KEEL-PROTO-006 rejection (301 to
// the notice), not net/http's built-in 400, and is counted.
func TestServeHTTPS_PlaintextGetsTheRejection(t *testing.T) {
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
	var rejected []protonotice.Code
	r := realRunner{onRejected: func(c protonotice.Code) {
		mu.Lock()
		defer mu.Unlock()
		rejected = append(rejected, c)
	}}
	cfg := shortDrainCfg()
	cfg.Listeners.HTTP.Enabled = true
	cfg.Listeners.HTTP.Port = 8080
	log := logging.New(logging.Config{Out: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.serveHTTPS(ctx, lifecycle.NewShutdownOrchestrator(log), "127.0.0.1:0", http.NotFoundHandler(), cfg, loader, nil, log)
	}()
	addr := <-addrCh

	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("plain HTTP to the HTTPS port: %v", err)
	}
	_ = resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	host, _, _ := net.SplitHostPort(addr)
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Keel-Error-Code") != string(protonotice.PlaintextOnHTTPS) ||
		resp.Header.Get("Location") != "http://"+host+":8080/.well-known/keel/upgrade?code=KEEL-PROTO-006" {
		t.Fatalf("got %d %v", resp.StatusCode, resp.Header)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rejected) != 1 || rejected[0] != protonotice.PlaintextOnHTTPS {
		t.Fatalf("counted %v, want [KEEL-PROTO-006]", rejected)
	}
}
