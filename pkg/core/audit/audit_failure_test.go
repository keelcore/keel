//go:build !no_audit

package audit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify_PrevHashMismatch(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Anchor{}, nil)
	l.Record(sample(1))
	l.Record(sample(2))
	second := entries(t, buf.Bytes())[1]
	tampered := strings.ReplaceAll(buf.String(), `"prev_hash":"`+second.PrevHash, `"prev_hash":"`+strings.Repeat("0", len(second.PrevHash)))
	if _, err := VerifyReader(strings.NewReader(tampered)); err == nil || !strings.Contains(err.Error(), "prev_hash") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyFiles_OpenFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := VerifyFiles([]string{filepath.Join(dir, "none")}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: err = %v", err)
	}
	gz := filepath.Join(dir, "audit.log.1.gz")
	if err := os.WriteFile(gz, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFiles([]string{gz}); err == nil {
		t.Fatal("bad gzip: want an error")
	}
}

func TestFiles_Failures(t *testing.T) {
	dir := t.TempDir()
	if _, err := Files(filepath.Join(dir, "audit[.log")); err == nil {
		t.Fatal("bad pattern: want an error")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Files(filepath.Join(file, "audit.log")); err == nil {
		t.Fatal("a path under a file: want an error")
	}
}

func TestFiles_SkipsTemporaryFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path+".1.gz.tmp", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Files(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("Files = %v, %v", got, err)
	}
}

func TestResume_Failures(t *testing.T) {
	dir := t.TempDir()
	if _, err := Resume(filepath.Join(dir, "audit[.log")); err == nil {
		t.Fatal("bad pattern: want an error")
	}
	dangling := filepath.Join(dir, "dangling.log")
	if err := os.Symlink(filepath.Join(dir, "none"), dangling+".1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(dangling); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dangling backup: err = %v", err)
	}
	long := filepath.Join(dir, "long.log")
	if err := os.WriteFile(long, bytes.Repeat([]byte("x"), 17*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(long); err == nil {
		t.Fatal("a line past the scanner limit: want an error")
	}
}

func TestVerify_NotJSON(t *testing.T) {
	if _, err := VerifyReader(strings.NewReader("not json\n")); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("err = %v", err)
	}
}
