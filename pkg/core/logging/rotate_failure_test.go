package logging

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failOpens makes rotateOpenFile run fn instead of opening, for names ending in suffix.
func failOpens(t *testing.T, suffix string, fn func(name string) (*os.File, error)) {
	t.Helper()
	orig := rotateOpenFile
	rotateOpenFile = func(name string, flag int, perm fs.FileMode) (*os.File, error) {
		if strings.HasSuffix(name, suffix) {
			return fn(name)
		}
		return orig(name, flag, perm)
	}
	t.Cleanup(func() { rotateOpenFile = orig })
}

// failRename makes every rotation rename fail, without touching the file: Windows cannot remove an open one.
func failRename(t *testing.T) error {
	t.Helper()
	boom := errors.New("rename boom")
	orig := rotateRename
	rotateRename = func(string, string) error { return boom }
	t.Cleanup(func() { rotateRename = orig })
	return boom
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
