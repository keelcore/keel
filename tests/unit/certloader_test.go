package unit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// writeCertFiles writes PEM cert and key to temp files and returns their paths.
func writeCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	certPEM, keyPEM := tlsTestCert(t, time.Now().Add(time.Hour))
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
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
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
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
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
	loader, err := keeltls.NewCertLoader(certFile, keyFile)
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