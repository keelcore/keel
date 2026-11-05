package logging

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func backups(t *testing.T, path string) []string {
	t.Helper()
	m, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newRotating(t *testing.T, p RotationPolicy) (*RotatingFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keel.log")
	r, err := NewRotatingFile(path, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, path
}

func write(t *testing.T, r *RotatingFile, s string) {
	t.Helper()
	if n, err := r.Write([]byte(s)); err != nil || n != len(s) {
		t.Fatalf("Write = %d, %v", n, err)
	}
}

func TestRotatingFile_CreatesOwnerOnlyAndAppends(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	write(t, r, "one\n")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 { // Windows has no POSIX modes
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	again, err := NewRotatingFile(path, RotationPolicy{MaxSizeMB: 1})
	if err != nil {
		t.Fatal(err)
	}
	write(t, again, "two\n")
	_ = again.Close()
	if b, _ := os.ReadFile(path); string(b) != "one\ntwo\n" {
		t.Fatalf("content = %q, want both lines", b)
	}
}

func TestRotatingFile_RotatesPastTheSizeLimit(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	line := strings.Repeat("x", 600*1024)
	write(t, r, line)
	if n := len(backups(t, path)); n != 0 {
		t.Fatalf("%d backups below the limit", n)
	}
	write(t, r, line) // would exceed 1 MB
	r.wait()
	got := backups(t, path)
	if len(got) != 1 {
		t.Fatalf("backups = %v, want 1", got)
	}
	if b, _ := os.ReadFile(got[0]); len(b) != len(line) {
		t.Fatalf("backup holds %d bytes, want %d", len(b), len(line))
	}
	if b, _ := os.ReadFile(path); len(b) != len(line) {
		t.Fatalf("current file holds %d bytes, want %d", len(b), len(line))
	}
}

// A line never splits, even one larger than the limit: it goes whole into a fresh file.
func TestRotatingFile_OversizeLineStaysWhole(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	line := strings.Repeat("x", 2*1024*1024)
	write(t, r, line)
	r.wait()
	if n := len(backups(t, path)); n != 0 {
		t.Fatalf("%d backups, want 0: an empty file is not rotated", n)
	}
	if b, _ := os.ReadFile(path); len(b) != len(line) {
		t.Fatalf("file holds %d bytes", len(b))
	}
}

func TestRotatingFile_RotateForced(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	r.wait()
	if n := len(backups(t, path)); n != 0 {
		t.Fatalf("rotating an empty file made %d backups", n)
	}
	write(t, r, "before\n")
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	write(t, r, "after\n")
	r.wait()
	got := backups(t, path)
	if len(got) != 1 {
		t.Fatalf("backups = %v", got)
	}
	if b, _ := os.ReadFile(got[0]); string(b) != "before\n" {
		t.Fatalf("backup = %q", b)
	}
	if b, _ := os.ReadFile(path); string(b) != "after\n" {
		t.Fatalf("current = %q", b)
	}
}

func TestRotatingFile_KeepsMaxBackups(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1, MaxBackups: 2})
	for range 5 {
		write(t, r, "line\n")
		if err := r.Rotate(); err != nil {
			t.Fatal(err)
		}
		r.wait()
	}
	if got := backups(t, path); len(got) != 2 {
		t.Fatalf("backups = %v, want the newest 2", got)
	}
}

func TestRotatingFile_RemovesBackupsPastMaxAge(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1, MaxAgeDays: 7})
	old := path + ".20200101T000000.000000000Z"
	if err := os.WriteFile(old, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	write(t, r, "line\n")
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	r.wait()
	got := backups(t, path)
	if len(got) != 1 || got[0] == old {
		t.Fatalf("backups = %v, want only the new one", got)
	}
}

func TestRotatingFile_Compresses(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1, Compress: true})
	write(t, r, "compress me\n")
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	r.wait()
	got := backups(t, path)
	if len(got) != 1 || !strings.HasSuffix(got[0], ".gz") {
		t.Fatalf("backups = %v, want one .gz", got)
	}
	f, err := os.Open(got[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "compress me\n" {
		t.Fatalf("decompressed = %q", b)
	}
	if info, _ := os.Stat(got[0]); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 { // Windows has no POSIX modes
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
}

// SIGHUP picks up a new policy on the open file.
func TestRotatingFile_SetPolicy(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 100})
	r.SetPolicy(RotationPolicy{MaxSizeMB: 1, Compress: true})
	write(t, r, strings.Repeat("x", 900*1024))
	write(t, r, strings.Repeat("y", 900*1024))
	r.wait()
	got := backups(t, path)
	if len(got) != 1 || !strings.HasSuffix(got[0], ".gz") {
		t.Fatalf("backups = %v, want one .gz", got)
	}
}

func TestRotatingFile_WriteAfterCloseIsDropped(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	_ = r.Close()
	if n, err := r.Write([]byte("late\n")); err != nil || n != 5 {
		t.Fatalf("Write after Close = %d, %v", n, err)
	}
	if err := r.Rotate(); err != nil {
		t.Fatalf("Rotate after Close: %v", err)
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatalf("file = %q", b)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestNewRotatingFile_OpenError(t *testing.T) {
	if _, err := NewRotatingFile(filepath.Join(t.TempDir(), "missing", "keel.log"), RotationPolicy{}); err == nil {
		t.Fatal("want an error for a path in a missing directory")
	}
}

// With a limit of 0 the file grows until a forced rotation.
func TestRotatingFile_ZeroSizeNeverRotatesBySize(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	write(t, r, strings.Repeat("x", 2*1024*1024))
	write(t, r, strings.Repeat("x", 2*1024*1024))
	r.wait()
	if n := len(backups(t, path)); n != 0 {
		t.Fatalf("%d backups", n)
	}
}
