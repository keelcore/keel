// tests/unit/tls_gaps_test.go
package unit

import (
	"os"
	"testing"
	"time"

	keeltls "github.com/keelcore/keel/pkg/core/tls"
)

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
