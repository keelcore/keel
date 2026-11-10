package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keelcore/keel/tests/unit"
)

// genCertKey writes a self-signed ECDSA cert and key into dir and returns the
// cert path, key path, and the certificate NotAfter time.
func genCertKey(t *testing.T, dir string, notAfter time.Time) (string, string, time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, notAfter
}

func TestCertExpiry(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("fips")
	}
	dir := t.TempDir()
	want := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	certPath, _, _ := genCertKey(t, dir, want)

	got, err := CertExpiry(certPath)
	if err != nil {
		t.Fatalf("CertExpiry: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("expiry = %v, want %v", got, want)
	}
}

func TestCertExpiryReadError(t *testing.T) {
	if _, err := CertExpiry(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCertExpiryNoPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(path, []byte("not a pem block"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CertExpiry(path); err == nil {
		t.Fatal("expected error for non-PEM content")
	}
}

func TestCertExpiryParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-der")})
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CertExpiry(path); err == nil {
		t.Fatal("expected error for unparseable cert")
	}
}

func TestCertExpirySeconds(t *testing.T) {
	if os.Getenv("GOFIPS140") != "" {
		t.Skip("fips")
	}
	dir := t.TempDir()
	certPath, _, _ := genCertKey(t, dir, time.Now().Add(24*time.Hour))

	secs, err := CertExpirySeconds(certPath)
	if err != nil {
		t.Fatalf("CertExpirySeconds: %v", err)
	}
	if secs <= 0 {
		t.Fatalf("seconds = %v, want > 0", secs)
	}
}

func TestCertExpirySecondsError(t *testing.T) {
	if _, err := CertExpirySeconds(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// CertExpiry: os.ReadFile fails for a non-existent path.
func TestCertExpiry_ReadFileError(t *testing.T) {
	_, err := CertExpiry("/no/such/file/cert.pem")
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

	_, err = CertExpiry(f.Name())
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

	_, err = CertExpiry(f.Name())
	if err == nil {
		t.Error("expected error for invalid DER certificate bytes, got nil")
	}
}

// CertExpirySeconds: propagates CertExpiry error (bad path).
func TestCertExpirySeconds_PropagatesError(t *testing.T) {
	_, err := CertExpirySeconds("/no/such/file.pem")
	if err == nil {
		t.Error("expected error from CertExpirySeconds, got nil")
	}
}

func TestCertExpiry_ReturnsNotAfter(t *testing.T) {
	notAfter := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	certPEM, _ := unit.TLSTestCert(t, notAfter)

	f, err := os.CreateTemp(t.TempDir(), "keel-cert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(certPEM); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := CertExpiry(f.Name())
	if err != nil {
		t.Fatalf("CertExpiry: %v", err)
	}
	if !got.Equal(notAfter) {
		t.Errorf("CertExpiry = %v, want %v", got, notAfter)
	}
}

func TestCertExpirySeconds_PositiveForFutureCert(t *testing.T) {
	certPEM, _ := unit.TLSTestCert(t, time.Now().Add(time.Hour))

	f, err := os.CreateTemp(t.TempDir(), "keel-cert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(certPEM); err != nil {
		t.Fatal(err)
	}
	f.Close()

	secs, err := CertExpirySeconds(f.Name())
	if err != nil {
		t.Fatalf("CertExpirySeconds: %v", err)
	}
	if secs <= 0 {
		t.Errorf("CertExpirySeconds = %f, want > 0 for a future cert", secs)
	}
}
