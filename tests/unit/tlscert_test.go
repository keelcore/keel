package unit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

func TestTLSTestCert_ValidPairForLoopbackUntilNotAfter(t *testing.T) {
	notAfter := time.Now().Add(time.Hour).Truncate(time.Second)
	certPEM, keyPEM := TLSTestCert(t, notAfter)
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("cert and key do not pair: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("certPEM is not a CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.NotAfter.Equal(notAfter) {
		t.Errorf("NotAfter = %v, want %v", cert.NotAfter, notAfter)
	}
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("IPAddresses = %v, want [127.0.0.1]", cert.IPAddresses)
	}
}

// fatalTB records the first Fatal and stops its goroutine, as testing.T does, without failing the test.
type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Fatal(args ...any) {
	f.msg = fmt.Sprint(args...)
	runtime.Goexit()
}

// fatalOf runs TLSTestCert against a fatalTB and returns what it reported.
func fatalOf() string {
	f := &fatalTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		TLSTestCert(f, time.Now().Add(time.Hour))
	}()
	<-done
	return f.msg
}

func TestTLSTestCert_FatalOnEachCryptoError(t *testing.T) {
	origKey, origCert, origMarshal := generateKey, createCertificate, marshalECPrivateKey
	t.Cleanup(func() { generateKey, createCertificate, marshalECPrivateKey = origKey, origCert, origMarshal })
	boom := errors.New("boom")

	generateKey = func(elliptic.Curve, io.Reader) (*ecdsa.PrivateKey, error) { return nil, boom }
	if got := fatalOf(); got != "boom" {
		t.Errorf("key generation error: Fatal %q, want boom", got)
	}
	generateKey = origKey

	createCertificate = func(io.Reader, *x509.Certificate, *x509.Certificate, any, any) ([]byte, error) {
		return nil, boom
	}
	if got := fatalOf(); got != "boom" {
		t.Errorf("certificate error: Fatal %q, want boom", got)
	}
	createCertificate = origCert

	marshalECPrivateKey = func(*ecdsa.PrivateKey) ([]byte, error) { return nil, boom }
	if got := fatalOf(); got != "boom" {
		t.Errorf("key marshal error: Fatal %q, want boom", got)
	}
}
