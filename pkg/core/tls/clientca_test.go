package tls

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadClientCAs(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadClientCAs(filepath.Join(dir, "missing.pem")); err == nil {
		t.Error("missing file: expected error")
	}
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClientCAs(empty); err == nil {
		t.Error("no certificates: expected error")
	}
	certPath, _, _ := genCertKey(t, dir, time.Now().Add(time.Hour))
	pool, err := LoadClientCAs(certPath)
	if err != nil || pool == nil {
		t.Fatalf("valid bundle: pool=%v err=%v", pool, err)
	}
}
