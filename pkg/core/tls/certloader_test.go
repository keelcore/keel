package tls

import (
	stdtls "crypto/tls"
	"os"
	"testing"
	"time"
)

func TestNewCertLoaderAndGet(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("fips")
	}
	dir := t.TempDir()
	certPath, keyPath, _ := genCertKey(t, dir, time.Now().Add(24*time.Hour))

	l, err := NewCertLoader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	got, err := l.Get(&stdtls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil certificate")
	}

	// Reload with a fresh cert should succeed and swap the stored cert.
	dir2 := t.TempDir()
	certPath2, keyPath2, _ := genCertKey(t, dir2, time.Now().Add(48*time.Hour))
	if err := l.Reload(certPath2, keyPath2); err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

func TestCertLoaderReloadError(t *testing.T) {
	if _, err := NewCertLoader("missing-cert.pem", "missing-key.pem"); err == nil {
		t.Fatal("expected error for missing cert/key")
	}
}

func TestCertLoaderGetNoCert(t *testing.T) {
	l := &CertLoader{}
	if _, err := l.Get(&stdtls.ClientHelloInfo{}); err == nil {
		t.Fatal("expected error when no certificate loaded")
	}
}

// CertLoader.Get: zero-value CertLoader has nil atomic.Pointer → "no certificate loaded".
func TestCertLoader_Get_NilCert(t *testing.T) {
	var l CertLoader
	_, err := l.Get(nil)
	if err == nil {
		t.Error("expected error from Get on zero-value CertLoader, got nil")
	}
}

// CertLoader.Reload: fails when cert/key files do not exist.
func TestCertLoader_Reload_Error(t *testing.T) {
	var l CertLoader
	err := l.Reload("/no/such/cert.pem", "/no/such/key.pem")
	if err == nil {
		t.Error("expected error from Reload with missing files, got nil")
	}
}

// NewCertLoader: fails when cert/key files do not exist.
func TestNewCertLoader_Error(t *testing.T) {
	_, err := NewCertLoader("/no/such/cert.pem", "/no/such/key.pem")
	if err == nil {
		t.Error("expected error from NewCertLoader with missing files, got nil")
	}
}
