//go:build !no_audit

// Package audit is keel's tamper-evident audit stream, separate from the access log: security
// events as JSON lines, each carrying a sequence number and the SHA-256 of the previous entry, so
// an edited, removed or reordered entry breaks the chain that `keel audit verify` checks.
package audit

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Event is what a caller records.
type Event struct {
	Event     string
	Principal string
	SourceIP  string
	Outcome   string
	Detail    string
}

// Entry is one line of the stream. Hash covers every other field, in this order.
type Entry struct {
	Seq       uint64 `json:"seq"`
	TS        string `json:"ts"`
	Event     string `json:"event"`
	Principal string `json:"principal,omitempty"`
	SourceIP  string `json:"source_ip,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Detail    string `json:"detail,omitempty"`
	PrevHash  string `json:"prev_hash"`
	Hash      string `json:"hash,omitempty"`
}

// Anchor is the chain's position: the last sequence number and hash.
type Anchor struct {
	Seq  uint64
	Hash string
}

// Log appends entries to a writer. A nil *Log records nothing.
type Log struct {
	mu      sync.Mutex
	w       io.Writer
	anchor  Anchor
	onError func(error)
}

// New starts a log continuing from anchor. onError (optional) receives write failures.
func New(w io.Writer, anchor Anchor, onError func(error)) *Log {
	return &Log{w: w, anchor: anchor, onError: onError}
}

// Anchor is the chain's current position.
func (l *Log) Anchor() Anchor {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.anchor
}

// Record appends e to the chain.
func (l *Log) Record(e Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := Entry{
		Seq: l.anchor.Seq + 1, TS: time.Now().UTC().Format(time.RFC3339Nano), Event: e.Event,
		Principal: e.Principal, SourceIP: e.SourceIP, Outcome: e.Outcome, Detail: e.Detail, PrevHash: l.anchor.Hash,
	}
	entry.Hash = entryHash(entry)
	line, _ := json.Marshal(entry) // strings and a number: cannot fail
	if _, err := l.w.Write(append(line, '\n')); err != nil {
		if l.onError != nil {
			l.onError(err)
		}
		return
	}
	l.anchor = Anchor{Seq: entry.Seq, Hash: entry.Hash}
}

func entryHash(e Entry) string {
	e.Hash = ""
	b, _ := json.Marshal(e) // strings and a number: cannot fail
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// verifier checks entries against the chain.
type verifier struct {
	anchor Anchor
	count  int
}

func (v *verifier) check(r io.Reader, source string) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("%s: entry after seq %d is not valid: %w", source, v.anchor.Seq, err)
		}
		if e.Seq != v.anchor.Seq+1 {
			return fmt.Errorf("%s: seq %d follows seq %d: an entry is missing or moved", source, e.Seq, v.anchor.Seq)
		}
		if e.PrevHash != v.anchor.Hash {
			return fmt.Errorf("%s: seq %d: prev_hash does not match the previous entry", source, e.Seq)
		}
		if e.Hash != entryHash(e) {
			return fmt.Errorf("%s: seq %d: hash does not match its content: the entry was altered", source, e.Seq)
		}
		v.anchor = Anchor{Seq: e.Seq, Hash: e.Hash}
		v.count++
	}
	return sc.Err()
}

// VerifyReader checks a whole chain from its first entry and returns how many entries it holds.
func VerifyReader(r io.Reader) (int, error) {
	var v verifier
	err := v.check(r, "input")
	return v.count, err
}

// VerifyFiles checks a chain across files given oldest first; .gz files are decompressed.
func VerifyFiles(paths []string) (int, error) {
	var v verifier
	for _, p := range paths {
		if err := v.checkFile(p); err != nil {
			return v.count, err
		}
	}
	return v.count, nil
}

func (v *verifier) checkFile(path string) error {
	f, closeFn, err := openEntries(path)
	if err != nil {
		return err
	}
	defer closeFn()
	return v.check(f, path)
}

func openEntries(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if !strings.HasSuffix(path, ".gz") {
		return f, func() { _ = f.Close() }, nil
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return zr, func() { _ = zr.Close(); _ = f.Close() }, nil
}

// Files lists a rotated stream oldest first: path's backups by name (their UTC time), then path.
func Files(path string) ([]string, error) {
	backups, err := backupsOf(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		backups = append(backups, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return backups, nil
}

func backupsOf(path string) ([]string, error) {
	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		if !strings.HasSuffix(m, ".tmp") {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Resume finds where a stream left off: the last entry of path, or of its newest backup when path
// is empty or missing; the zero Anchor for a new stream. An unreadable tail is an error, so a
// restart never silently starts a second chain.
func Resume(path string) (Anchor, error) {
	files, err := Files(path)
	if err != nil {
		return Anchor{}, err
	}
	for i := len(files) - 1; i >= 0; i-- {
		last, ok, err := lastEntry(files[i])
		if err != nil {
			return Anchor{}, err
		}
		if ok {
			return Anchor{Seq: last.Seq, Hash: last.Hash}, nil
		}
	}
	return Anchor{}, nil
}

func lastEntry(path string) (Entry, bool, error) {
	r, closeFn, err := openEntries(path)
	if err != nil {
		return Entry{}, false, err
	}
	defer closeFn()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var last []byte
	for sc.Scan() {
		last = append(last[:0], sc.Bytes()...)
	}
	if err := sc.Err(); err != nil {
		return Entry{}, false, fmt.Errorf("%s: %w", path, err)
	}
	if len(last) == 0 {
		return Entry{}, false, nil
	}
	var e Entry
	if err := json.Unmarshal(last, &e); err != nil {
		return Entry{}, false, fmt.Errorf("%s: last entry is not valid: %w", path, err)
	}
	return e, true, nil
}
