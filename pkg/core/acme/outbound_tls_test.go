//go:build !no_acme

package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/keelcore/keel/pkg/config"
)

func assertOutboundPolicy(t *testing.T, name string, c *http.Client) *tls.Config {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("%s: transport = %#v, want the outbound TLS policy", name, c.Transport)
	}
	return tr.TLSClientConfig
}

// The CA client gets the policy, with or without a custom CA, and keeps the custom roots.
func TestACMEClient_UsesTheOutboundTLSPolicy(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := buildACMEClient(key, config.ACMEConfig{})
	if err != nil {
		t.Fatal(err)
	}
	assertOutboundPolicy(t, "default CA", plain.(*xacme.Client).HTTPClient)

	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	custom, err := buildACMEClient(key, config.ACMEConfig{CACertFile: caFile})
	if err != nil {
		t.Fatal(err)
	}
	if assertOutboundPolicy(t, "custom CA", custom.(*xacme.Client).HTTPClient).RootCAs == nil {
		t.Fatal("custom CA: RootCAs dropped")
	}
}
