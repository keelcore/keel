package unit

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
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
