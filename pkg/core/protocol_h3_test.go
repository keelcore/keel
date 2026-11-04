//go:build !no_h3

package core

import (
	"context"
	cryptotls "crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	qhttp3 "github.com/quic-go/quic-go/http3"

	"github.com/keelcore/keel/pkg/core/protonotice"
	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// The H3 listener runs with the rejection observer installed and serves an
// allowed client without reporting it.
func TestServeH3_ObservedServesAllowedClients(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.LocalAddr().String()
	_ = probe.Close()

	var reported atomic.Int32
	r := realRunner{onReject: func(protonotice.Code, *cryptotls.ClientHelloInfo) { reported.Add(1) }}
	r.h3Cert = loader.Get
	bound := make(chan struct{})
	r.onBound = func() { close(bound) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.serveH3(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "h3-ok")
		}), shortDrainCfg(), discardLog())
	}()
	<-bound

	tr := &qhttp3.Transport{TLSClientConfig: &cryptotls.Config{RootCAs: roots}}
	defer tr.Close()
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://" + addr + "/")
	if err != nil {
		cancel()
		t.Fatalf("H3 GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if string(body) != "h3-ok" || reported.Load() != 0 {
		t.Fatalf("body %q, reported %d; want h3-ok and no rejection", body, reported.Load())
	}
}
