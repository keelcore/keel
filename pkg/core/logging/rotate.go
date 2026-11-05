package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RotationPolicy bounds a RotatingFile. A zero value in any field turns that limit off.
type RotationPolicy struct {
	MaxSizeMB  int  // rotate before a write would pass this size
	MaxAgeDays int  // remove backups older than this
	MaxBackups int  // keep at most this many backups
	Compress   bool // gzip each backup
}

// backupTime names a backup: sorted by name is sorted by age.
const backupTime = "20060102T150405.000000000Z"

// rotateOpenFile is a seam over os.OpenFile so tests can inject open failures.
var rotateOpenFile = os.OpenFile

// rotateRename is a seam over os.Rename so tests can inject rename failures.
var rotateRename = os.Rename

// RotatingFile is an io.Writer on a log file that rotates by size or on demand. Rotation renames
// the file to <path>.<UTC time> and opens a new one; compression and pruning of backups run in
// the background, so a write never waits for them. Safe for concurrent use. Whole lines stay
// whole: each Write goes entirely into one file.
type RotatingFile struct {
	path string

	mu     sync.Mutex
	policy RotationPolicy
	f      *os.File
	size   int64
	closed bool

	cleanup sync.WaitGroup
}

// NewRotatingFile opens path for appending, creating it owner-only if needed.
func NewRotatingFile(path string, p RotationPolicy) (*RotatingFile, error) {
	r := &RotatingFile{path: path, policy: p}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := rotateOpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	size, _ := f.Seek(0, io.SeekEnd) // on failure, a size of 0 only delays a rotation
	r.f, r.size = f, size
	return nil
}

// Write appends b, rotating first when b would pass the size limit. A rotation that fails is
// reported on stderr (a logger cannot log its own output failing), and the write goes to the
// current file.
func (r *RotatingFile) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return len(b), nil
	}
	if limit := int64(r.policy.MaxSizeMB) * 1024 * 1024; limit > 0 && r.size > 0 && r.size+int64(len(b)) > limit {
		if err := r.rotateLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation failed: %v\n", err)
		}
	}
	n, err := r.f.Write(b)
	r.size += int64(n)
	return n, err
}

// Rotate rotates now (SIGUSR2). An empty file stays as it is.
func (r *RotatingFile) Rotate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.size == 0 {
		return nil
	}
	return r.rotateLocked()
}

func (r *RotatingFile) rotateLocked() error {
	backup := r.path + "." + time.Now().UTC().Format(backupTime)
	// Close first: Windows refuses to rename an open file.
	if err := r.f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "log file close failed: %v\n", err)
	}
	if err := rotateRename(r.path, backup); err != nil {
		if oerr := r.open(); oerr != nil {
			r.closed = true
		}
		return err
	}
	if err := r.open(); err != nil {
		// Put the file back, reopened, so logging carries on where it was.
		if rerr := os.Rename(backup, r.path); rerr == nil {
			if oerr := r.open(); oerr == nil {
				return err
			}
		}
		r.closed = true
		return err
	}
	policy := r.policy
	r.cleanup.Add(1)
	go func() {
		defer r.cleanup.Done()
		r.finishBackup(backup, policy)
	}()
	return nil
}

// finishBackup compresses the new backup and prunes old ones. Failures go to stderr.
func (r *RotatingFile) finishBackup(backup string, p RotationPolicy) {
	if p.Compress {
		if err := compressFile(backup); err != nil {
			fmt.Fprintf(os.Stderr, "log backup compression failed: %v\n", err)
		}
	}
	if err := r.prune(p); err != nil {
		fmt.Fprintf(os.Stderr, "log backup pruning failed: %v\n", err)
	}
}

// compressFile writes path.gz owner-only, through a temporary name so a partial file is never
// taken for a backup, then removes path.
func compressFile(path string) (err error) {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path + ".gz.tmp"
	out, err := rotateOpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
		}
	}()
	zw := gzip.NewWriter(out)
	if _, err = io.Copy(zw, in); err != nil {
		return err
	}
	if err = errors.Join(zw.Close(), out.Close()); err != nil {
		return err
	}
	if err = os.Rename(tmp, path+".gz"); err != nil {
		return err
	}
	_ = in.Close() // Windows refuses to remove an open file
	return os.Remove(path)
}

// prune removes backups past MaxAgeDays and beyond the newest MaxBackups.
func (r *RotatingFile) prune(p RotationPolicy) error {
	matches, err := filepath.Glob(r.path + ".*")
	if err != nil {
		return err
	}
	var all []string
	for _, m := range matches {
		if !strings.HasSuffix(m, ".tmp") {
			all = append(all, m)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(all))) // newest first
	var firstErr error
	for i, m := range all {
		if !r.expired(m, i, p) {
			continue
		}
		if err := os.Remove(m); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *RotatingFile) expired(path string, index int, p RotationPolicy) bool {
	if p.MaxBackups > 0 && index >= p.MaxBackups {
		return true
	}
	if p.MaxAgeDays <= 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) > time.Duration(p.MaxAgeDays)*24*time.Hour
}

// SetPolicy applies a new policy to later writes and rotations (SIGHUP).
func (r *RotatingFile) SetPolicy(p RotationPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = p
}

// wait blocks until background compression and pruning finish.
func (r *RotatingFile) wait() {
	r.cleanup.Wait()
}

// Close waits for background work and closes the file. Later writes are dropped.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.wait()
		return nil
	}
	r.closed = true
	err := r.f.Close()
	r.mu.Unlock()
	r.wait()
	return err
}
