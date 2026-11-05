//go:build !no_audit

package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

func sample(n int) Event {
	return Event{Event: "authn_failure", Principal: "alice", SourceIP: "203.0.113.7", Outcome: "deny", Detail: strings.Repeat("x", n)}
}

func entries(t *testing.T, b []byte) []Entry {
	t.Helper()
	var out []Entry
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e Entry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		out = append(out, e)
	}
	return out
}

func TestRecord_ChainsEntries(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Anchor{}, nil)
	l.Record(sample(0))
	l.Record(Event{Event: "config_reload", Outcome: "ok"})
	got := entries(t, buf.Bytes())
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("entries = %+v", got)
	}
	if got[0].PrevHash != "" || got[1].PrevHash != got[0].Hash || got[0].Hash == "" {
		t.Fatalf("chain broken: %+v", got)
	}
	if got[0].Event != "authn_failure" || got[0].Principal != "alice" || got[0].SourceIP != "203.0.113.7" ||
		got[0].Outcome != "deny" || got[0].TS == "" {
		t.Fatalf("fields = %+v", got[0])
	}
}

func TestVerify_AcceptsAnIntactChain(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Anchor{}, nil)
	for range 5 {
		l.Record(sample(0))
	}
	n, err := VerifyReader(bytes.NewReader(buf.Bytes()))
	if err != nil || n != 5 {
		t.Fatalf("VerifyReader = %d, %v", n, err)
	}
}

func TestVerify_DetectsTampering(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Anchor{}, nil)
	for range 4 {
		l.Record(sample(0))
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	for name, bad := range map[string][]string{
		"edited":  {lines[0], strings.Replace(lines[1], "alice", "mallory", 1), lines[2], lines[3]},
		"deleted": {lines[0], lines[2], lines[3]},
		"swapped": {lines[0], lines[2], lines[1], lines[3]},
		"garbage": {lines[0], "not json", lines[2]},
	} {
		if _, err := VerifyReader(strings.NewReader(strings.Join(bad, "\n") + "\n")); err == nil {
			t.Errorf("%s: verified, want an error", name)
		}
	}
	// A truncated head is caught: the first entry must start the chain unless a file came before it.
	if _, err := VerifyFiles(writeTemp(t, strings.Join(lines[1:], "\n")+"\n")); err == nil {
		t.Error("head removed: verified, want an error")
	}
}

func writeTemp(t *testing.T, content string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{p}
}

// Rotation carries the chain across files, compressed or not; a missing file breaks it.
func TestVerifyFiles_AcrossRotation(t *testing.T) {
	for _, compress := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "audit.log")
		f, err := logging.NewRotatingFile(path, logging.RotationPolicy{Compress: compress})
		if err != nil {
			t.Fatal(err)
		}
		l := New(f, Anchor{}, nil)
		for range 2 {
			l.Record(sample(0))
			l.Record(sample(0))
			if err := f.Rotate(); err != nil {
				t.Fatal(err)
			}
		}
		l.Record(sample(0))
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		files, err := Files(path)
		if err != nil || len(files) != 3 {
			t.Fatalf("compress=%v: Files = %v, %v", compress, files, err)
		}
		n, err := VerifyFiles(files)
		if err != nil || n != 5 {
			t.Fatalf("compress=%v: VerifyFiles = %d, %v", compress, n, err)
		}
		if _, err := VerifyFiles([]string{files[0], files[2]}); err == nil {
			t.Fatalf("compress=%v: a missing middle file verified", compress)
		}
	}
}

// A restart continues the chain from the file, or from the newest backup when the file is empty.
func TestResume(t *testing.T) {
	for _, compress := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "audit.log")
		if a, err := Resume(path); err != nil || a != (Anchor{}) {
			t.Fatalf("no file: %+v, %v", a, err)
		}
		f, err := logging.NewRotatingFile(path, logging.RotationPolicy{Compress: compress})
		if err != nil {
			t.Fatal(err)
		}
		l := New(f, Anchor{}, nil)
		l.Record(sample(0))
		l.Record(sample(0))
		want := l.Anchor()
		a, err := Resume(path)
		if err != nil || a != want {
			t.Fatalf("resume from file: %+v, %v; want %+v", a, err, want)
		}
		if err := f.Rotate(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		a, err = Resume(path)
		if err != nil || a != want {
			t.Fatalf("compress=%v: resume from backup: %+v, %v; want %+v", compress, a, err, want)
		}
	}
}

func TestResume_UnreadableTailIsAnError(t *testing.T) {
	p := writeTemp(t, "not json\n")[0]
	if _, err := Resume(p); err == nil {
		t.Fatal("want an error rather than silently starting a new chain")
	}
}

func TestRecord_NilLogIsANoOp(t *testing.T) {
	var l *Log
	l.Record(sample(0))
}

func TestRecord_ReportsWriteFailures(t *testing.T) {
	var got error
	l := New(failingWriter{}, Anchor{}, func(err error) { got = err })
	l.Record(sample(0))
	if got == nil {
		t.Fatal("write failure not reported")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }
