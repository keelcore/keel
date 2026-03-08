// tests/unit/tls_gaps_test.go
package unit

import (
	"encoding/pem"
	"os"
	"testing"
	"time"

	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

// CertExpiry: os.ReadFile fails for a non-existent path.
func TestCertExpiry_ReadFileError(t *testing.T) {
	_, err := keeltls.CertExpiry("/no/such/file/cert.pem")
	if err == nil {
		t.Error("expected error reading non-existent cert file, got nil")
	}
}

// CertExpiry: file exists but contains no PEM block.
func TestCertExpiry_NoPEMBlock(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cert*.pem")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not pem data\n")
	f.Close()

	_, err = keeltls.CertExpiry(f.Name())
	if err == nil {
		t.Error("expected error for file with no PEM block, got nil")
	}
}

// CertExpiry: PEM block present but bytes are not a valid DER certificate.
func TestCertExpiry_InvalidDER(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cert*.pem")
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "CERTIFICATE", Bytes: []byte("not valid DER")}
	_ = pem.Encode(f, block)
	f.Close()

	_, err = keeltls.CertExpiry(f.Name())
	if err == nil {
		t.Error("expected error for invalid DER certificate bytes, got nil")
	}
}

// CertExpirySeconds: propagates CertExpiry error (bad path).
func TestCertExpirySeconds_PropagatesError(t *testing.T) {
	_, err := keeltls.CertExpirySeconds("/no/such/file.pem")
	if err == nil {
		t.Error("expected error from CertExpirySeconds, got nil")
	}
}

// CertLoader.Get: zero-value CertLoader has nil atomic.Pointer → "no certificate loaded".
func TestCertLoader_Get_NilCert(t *testing.T) {
	var l keeltls.CertLoader
	_, err := l.Get(nil)
	if err == nil {
		t.Error("expected error from Get on zero-value CertLoader, got nil")
	}
}

// CertLoader.Reload: fails when cert/key files do not exist.
func TestCertLoader_Reload_Error(t *testing.T) {
	var l keeltls.CertLoader
	err := l.Reload("/no/such/cert.pem", "/no/such/key.pem")
	if err == nil {
		t.Error("expected error from Reload with missing files, got nil")
	}
}

// NewCertLoader: fails when cert/key files do not exist.
func TestNewCertLoader_Error(t *testing.T) {
	_, err := keeltls.NewCertLoader("/no/such/cert.pem", "/no/such/key.pem")
	if err == nil {
		t.Error("expected error from NewCertLoader with missing files, got nil")
	}
}

// CertLoader.Get: returns the stored certificate after a successful Reload.
func TestCertLoader_Get_ReturnsCert(t *testing.T) {
	certPEM, keyPEM := tlsTestCert(t, time.Now().Add(time.Hour))
	certFile := tlsWriteTemp(t, "cert*.pem", certPEM)
	keyFile := tlsWriteTemp(t, "key*.pem", keyPEM)

	l, err := keeltls.NewCertLoader(certFile, keyFile)
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

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