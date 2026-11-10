package keel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/audit"
)

func TestRunAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	l := audit.New(f, audit.Anchor{}, nil)
	l.Record(audit.Event{Event: "one", Principal: "alice"})
	l.Record(audit.Event{Event: "two"})
	_ = f.Close()

	var out, errOut bytes.Buffer
	if code := runAudit([]string{"verify", path}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "2 entries") {
		t.Fatalf("verify: code %d, out %q, err %q", code, out.String(), errOut.String())
	}
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, bytes.Replace(b, []byte("alice"), []byte("mallory"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runAudit([]string{"verify", path}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "altered") {
		t.Fatalf("tampered: code %d, out %q, err %q", code, out.String(), errOut.String())
	}
	for _, args := range [][]string{nil, {"verify"}, {"nope", path}} {
		errOut.Reset()
		if code := runAudit(args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage") {
			t.Fatalf("%v: code %d, err %q", args, code, errOut.String())
		}
	}
}
