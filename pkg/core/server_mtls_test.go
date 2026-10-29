package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/lifecycle"
	"github.com/keelcore/keel/pkg/core/logging"
)

// With tls.client_ca_file set, the handshake requires a client certificate
// chaining to that CA; without the file, client certs are not requested.
func TestApplyClientCA_RequiresVerifiedClientCert(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" || strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
		t.Skip("self-signed certs not permitted under FIPS 140-only mode")
	}
	var none cryptotls.Config
	if err := applyClientCA(&none, ""); err != nil || none.ClientAuth != cryptotls.NoClientCert {
		t.Fatalf("unset file: err=%v auth=%v", err, none.ClientAuth)
	}

	caFile, clientPair := newClientCA(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &cryptotls.Config{MinVersion: cryptotls.VersionTLS13}
	if err := applyClientCA(srv.TLS, caFile); err != nil {
		t.Fatal(err)
	}
	srv.StartTLS()
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	get := func(certs []cryptotls.Certificate) error {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: &cryptotls.Config{RootCAs: roots, Certificates: certs}}}
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get(nil); err == nil {
		t.Fatal("request without a client certificate succeeded")
	}
	if err := get([]cryptotls.Certificate{clientPair}); err != nil {
		t.Fatalf("request with a trusted client certificate failed: %v", err)
	}
}

// An unreadable client CA fails the HTTPS listener before it binds.
func TestServeHTTPSBound_BadClientCAFails(t *testing.T) {
	var cfg config.Config
	cfg.TLS.ClientCAFile = filepath.Join(t.TempDir(), "missing-ca.pem")
	log := logging.New(logging.Config{Out: io.Discard})
	fired := false
	err := serveHTTPSBound(context.Background(), lifecycle.NewShutdownOrchestrator(log), "127.0.0.1:0",
		http.NotFoundHandler(), cfg, nil, nil, log, func() { fired = true })
	if err == nil || !strings.Contains(err.Error(), "client_ca_file") {
		t.Fatalf("err = %v, want client_ca_file error", err)
	}
	if fired {
		t.Fatal("listener reported bound despite the bad client CA")
	}
}

// newClientCA writes a CA certificate to a temp file and returns it with a
// client certificate (ExtKeyUsageClientAuth) that the CA signed.
func newClientCA(t *testing.T) (string, cryptotls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test client CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(cryptorand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "client"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(cryptorand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "client-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile, cryptotls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
}
