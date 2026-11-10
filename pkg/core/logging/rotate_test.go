package logging

import (
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
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

// A rename that stays busy gives up with the error, and another error is not retried.
func TestRenameRetrying_GivesUp(t *testing.T) {
	busy := errors.New("busy")
	calls := 0
	origRename, origBusy, origDelay := rotateRename, renameBusy, renameRetryDelay
	rotateRename = func(string, string) error {
		calls++
		return busy
	}
	renameBusy = func(err error) bool { return errors.Is(err, busy) }
	renameRetryDelay = 0
	t.Cleanup(func() { rotateRename, renameBusy, renameRetryDelay = origRename, origBusy, origDelay })
	if err := renameRetrying("a", "b"); !errors.Is(err, busy) {
		t.Fatalf("err = %v", err)
	}
	if calls != renameRetries {
		t.Fatalf("calls = %d, want %d", calls, renameRetries)
	}
	calls = 0
	other := errors.New("other")
	rotateRename = func(string, string) error {
		calls++
		return other
	}
	if err := renameRetrying("a", "b"); !errors.Is(err, other) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

// A rotation that fails leaves the write in the current file.
func TestRotatingFile_WriteSurvivesAFailedRotation(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{MaxSizeMB: 1})
	write(t, r, "first\n")
	failRename(t)
	write(t, r, strings.Repeat("x", 1024*1024))
	if got := backups(t, path); len(got) != 0 {
		t.Fatalf("backups = %v, want none", got)
	}
}

func TestNewRotatingFile_OpenError(t *testing.T) {
	if _, err := NewRotatingFile(filepath.Join(t.TempDir(), "missing", "keel.log"), RotationPolicy{}); err == nil {
		t.Fatal("want an error for a path in a missing directory")
	}
}

// A close failure is reported, and the rotation still completes.
func TestRotatingFile_RotationSurvivesACloseFailure(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	if err := r.f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	write(t, r, "second\n")
	if got := backups(t, path); len(got) != 1 {
		t.Fatalf("backups = %v, want 1", got)
	}
}

// When the new file cannot be opened, the old one goes back and logging carries on in it.
func TestRotatingFile_ReopenFailurePutsTheFileBack(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	boom := errors.New("open boom")
	orig := rotateOpenFile
	calls := 0
	rotateOpenFile = func(name string, flag int, perm fs.FileMode) (*os.File, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return orig(name, flag, perm)
	}
	t.Cleanup(func() { rotateOpenFile = orig })
	if err := r.Rotate(); !errors.Is(err, boom) {
		t.Fatalf("Rotate = %v, want %v", err, boom)
	}
	write(t, r, "second\n")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Fatalf("file = %q", got)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("backups = %v, want none", b)
	}
}

// When the file can be neither replaced nor reopened, later writes are dropped.
func TestRotatingFile_ReopenFailureTwiceStopsWriting(t *testing.T) {
	r, _ := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	boom := errors.New("open boom")
	failOpens(t, "keel.log", func(string) (*os.File, error) { return nil, boom })
	if err := r.Rotate(); !errors.Is(err, boom) {
		t.Fatalf("Rotate = %v, want %v", err, boom)
	}
	write(t, r, "dropped\n")
	if !r.closed {
		t.Fatal("want the file marked closed")
	}
}

// When the backup cannot be renamed back, later writes are dropped.
func TestRotatingFile_ReopenFailureWithBackupGoneStopsWriting(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	boom := errors.New("open boom")
	failOpens(t, "keel.log", func(string) (*os.File, error) {
		matches, _ := filepath.Glob(path + ".*")
		for _, m := range matches {
			_ = os.Remove(m)
		}
		return nil, boom
	})
	if err := r.Rotate(); !errors.Is(err, boom) {
		t.Fatalf("Rotate = %v, want %v", err, boom)
	}
	if !r.closed {
		t.Fatal("want the file marked closed")
	}
}

// Compression and pruning failures leave the rotation itself intact.
func TestRotatingFile_BackgroundFailures(t *testing.T) {
	t.Run("compression", func(t *testing.T) {
		r, path := newRotating(t, RotationPolicy{Compress: true})
		write(t, r, "first\n")
		failOpens(t, ".gz.tmp", func(string) (*os.File, error) { return nil, errors.New("open boom") })
		if err := r.Rotate(); err != nil {
			t.Fatal(err)
		}
		r.wait()
		got := backups(t, path)
		if len(got) != 1 || strings.HasSuffix(got[0], ".gz") {
			t.Fatalf("backups = %v, want the one uncompressed", got)
		}
	})
	t.Run("pruning", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "keel[.log") // not a valid glob pattern
		r, err := NewRotatingFile(path, RotationPolicy{MaxBackups: 1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		write(t, r, "first\n")
		if err := r.Rotate(); err != nil {
			t.Fatal(err)
		}
		r.wait()
	})
}

func TestCompressFile_Failures(t *testing.T) {
	boom := errors.New("open boom")
	source := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "keel.log.1")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Run("missing source", func(t *testing.T) {
		if err := compressFile(filepath.Join(t.TempDir(), "none")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("temporary file", func(t *testing.T) {
		failOpens(t, ".gz.tmp", func(string) (*os.File, error) { return nil, boom })
		if err := compressFile(source(t, "x")); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unreadable source", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "keel.log.1") // opens, and reading it fails
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := compressFile(dir); err == nil {
			t.Fatal("want an error")
		}
		if _, err := os.Stat(dir + ".gz.tmp"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the temporary file stayed behind: %v", err)
		}
	})
	t.Run("unwritable output", func(t *testing.T) {
		failOpens(t, ".gz.tmp", func(name string) (*os.File, error) {
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				return nil, err
			}
			return os.Open(name) // read-only: the gzip header cannot be written
		})
		if err := compressFile(source(t, "")); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("final rename", func(t *testing.T) {
		path := source(t, "x")
		if err := os.MkdirAll(filepath.Join(path+".gz", "occupied"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := compressFile(path); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestRotatingFile_CloseTwice(t *testing.T) {
	r, _ := newRotating(t, RotationPolicy{})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

// A rename that fails reopens the path, so logging carries on in a file that exists.
func TestRotatingFile_RenameFailureReopens(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	boom := failRename(t)
	if err := r.Rotate(); !errors.Is(err, boom) {
		t.Fatalf("Rotate = %v", err)
	}
	write(t, r, "second\n")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Fatalf("file = %q", got)
	}
}

// When the rename fails and the path cannot be reopened, later writes are dropped.
func TestRotatingFile_RenameAndReopenFailureStopsWriting(t *testing.T) {
	r, _ := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	boom := failRename(t)
	failOpens(t, "keel.log", func(string) (*os.File, error) { return nil, errors.New("open boom") })
	if err := r.Rotate(); !errors.Is(err, boom) {
		t.Fatalf("Rotate = %v", err)
	}
	if !r.closed {
		t.Fatal("want the file marked closed")
	}
}

// Rotation closes the file before renaming it: Windows refuses to rename an open file.
func TestRotatingFile_ClosesBeforeRenaming(t *testing.T) {
	r, _ := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	old := r.f
	var atRename error
	orig := rotateRename
	rotateRename = func(from, to string) error {
		_, atRename = old.Write([]byte("x"))
		return orig(from, to)
	}
	t.Cleanup(func() { rotateRename = orig })
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(atRename, os.ErrClosed) {
		t.Fatalf("write at rename: err = %v, want the file closed", atRename)
	}
}

// A rename the system refuses for a moment (a scanner holding the new backup) is tried again.
func TestRotatingFile_RenameRetriesWhileBusy(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	write(t, r, "first\n")
	busy := errors.New("busy")
	calls := 0
	origRename, origBusy := rotateRename, renameBusy
	rotateRename = func(from, to string) error {
		calls++
		if calls < 3 {
			return busy
		}
		return origRename(from, to)
	}
	renameBusy = func(err error) bool { return errors.Is(err, busy) }
	t.Cleanup(func() { rotateRename, renameBusy = origRename, origBusy })
	if err := r.Rotate(); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("rename calls = %d, want 3", calls)
	}
	if got := backups(t, path); len(got) != 1 {
		t.Fatalf("backups = %v, want 1", got)
	}
}

// freezeRotateNow makes every rotation see the same instant, as two rotations within one clock
// tick do (Windows ticks coarsely).
func freezeRotateNow(t *testing.T, at time.Time) {
	t.Helper()
	orig := rotateNow
	rotateNow = func() time.Time { return at }
	t.Cleanup(func() { rotateNow = orig })
}

// backupText reads a backup, compressed or not.
func backupText(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rd io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		rd = zr
	}
	b, err := io.ReadAll(rd)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Rotations at the same instant keep every backup, each under its own name, in age order.
func TestRotate_SameInstantKeepsEveryBackup(t *testing.T) {
	for _, compress := range []bool{false, true} {
		freezeRotateNow(t, time.Date(2026, 11, 10, 5, 55, 16, 875597000, time.UTC))
		r, path := newRotating(t, RotationPolicy{Compress: compress})
		for _, line := range []string{"one\n", "two\n", "three\n"} {
			write(t, r, line)
			if err := r.Rotate(); err != nil {
				t.Fatal(err)
			}
		}
		r.wait()
		got := backups(t, path)
		if len(got) != 3 {
			t.Fatalf("compress=%v: backups = %v, want 3", compress, got)
		}
		for i, want := range []string{"one\n", "two\n", "three\n"} {
			if text := backupText(t, got[i]); text != want {
				t.Errorf("compress=%v: backup %d (%s) = %q, want %q (oldest first)", compress, i, got[i], text, want)
			}
		}
	}
}

// A rotation never replaces a file already at its backup name: a backup, its compressed form,
// or a compression in progress.
func TestRotate_NeverReplacesAnExistingBackup(t *testing.T) {
	at := time.Date(2026, 11, 10, 5, 55, 16, 875597000, time.UTC)
	for _, suffix := range []string{"", ".gz", ".gz.tmp"} {
		freezeRotateNow(t, at)
		r, path := newRotating(t, RotationPolicy{})
		taken := path + "." + at.Format(backupTime) + suffix
		if err := os.WriteFile(taken, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		write(t, r, "new\n")
		if err := r.Rotate(); err != nil {
			t.Fatal(err)
		}
		r.wait()
		if text, err := os.ReadFile(taken); err != nil || string(text) != "old\n" {
			t.Fatalf("suffix %q: existing %s = %q, %v; want it untouched", suffix, taken, text, err)
		}
		var fresh []string
		for _, b := range backups(t, path) {
			if b != taken {
				fresh = append(fresh, b)
			}
		}
		if len(fresh) != 1 || backupText(t, fresh[0]) != "new\n" {
			t.Fatalf("suffix %q: new backups = %v, want one holding the rotated lines", suffix, fresh)
		}
		if fresh[0] <= strings.TrimSuffix(taken, suffix) {
			t.Errorf("suffix %q: new backup %s sorts before the existing %s", suffix, fresh[0], taken)
		}
	}
}

// A backup that cannot be removed is reported, after the rest are handled.
func TestPrune_ReportsARemovalFailure(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	if err := os.MkdirAll(filepath.Join(path+".1", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".2", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.prune(RotationPolicy{MaxBackups: 1}); err == nil {
		t.Fatal("want an error")
	}
}

func TestPrune_SkipsTemporaryFiles(t *testing.T) {
	r, path := newRotating(t, RotationPolicy{})
	if err := os.WriteFile(path+".1.gz.tmp", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.prune(RotationPolicy{MaxBackups: 0, MaxAgeDays: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1.gz.tmp"); err != nil {
		t.Fatal(err)
	}
}
