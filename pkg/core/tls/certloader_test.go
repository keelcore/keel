package tls

import (
	stdtls "crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keelcore/keel/tests/unit"
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

// writeCertFiles writes PEM cert and key to temp files and returns their paths.
func writeCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	certPEM, keyPEM := unit.TLSTestCert(t, time.Now().Add(time.Hour))
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	return
}

func TestCertLoader_LoadsInitialCert(t *testing.T) {
	certFile, keyFile := writeCertFiles(t)
	loader, err := NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	cert, err := loader.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cert == nil {
		t.Error("expected non-nil certificate")
	}
}

func TestCertLoader_ReloadReplacesActiveCert(t *testing.T) {
	certFile, keyFile := writeCertFiles(t)
	loader, err := NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	cert1, _ := loader.Get(nil)

	// Generate a second distinct cert and reload.
	certFile2, keyFile2 := writeCertFiles(t)
	if err := loader.Reload(certFile2, keyFile2); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	cert2, _ := loader.Get(nil)

	if cert1 == cert2 {
		t.Error("expected different certificate pointer after reload")
	}
}

func TestCertLoader_ReloadBadPath_KeepsOldCert(t *testing.T) {
	certFile, keyFile := writeCertFiles(t)
	loader, err := NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	cert1, _ := loader.Get(nil)

	// Reload from non-existent path → error; old cert must remain.
	if err := loader.Reload("/no/such/cert.pem", "/no/such/key.pem"); err == nil {
		t.Fatal("expected error from bad reload path")
	}
	cert2, _ := loader.Get(nil)
	if cert1 != cert2 {
		t.Error("expected unchanged certificate after failed reload")
	}
}

// CertLoader.Get: returns the stored certificate after a successful Reload.
func TestCertLoader_Get_ReturnsCert(t *testing.T) {
	certPEM, keyPEM := unit.TLSTestCert(t, time.Now().Add(time.Hour))
	certFile := tlsWriteTemp(t, "cert*.pem", certPEM)
	keyFile := tlsWriteTemp(t, "key*.pem", keyPEM)

	l, err := NewCertLoader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewCertLoader: %v", err)
	}
	cert, err := l.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cert == nil {
		t.Error("expected non-nil certificate from Get")
	}
}

func tlsWriteTemp(t *testing.T, pattern string, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), pattern)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}
