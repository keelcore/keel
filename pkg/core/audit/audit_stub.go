//go:build no_audit

// Package audit is compiled out by the no_audit build tag.
package audit

import (
	"errors"
	"io"
)

// Event is what a caller records.
type Event struct {
	Event     string
	Principal string
	SourceIP  string
	Outcome   string
	Detail    string
}

// Anchor is the chain's position.
type Anchor struct {
	Seq  uint64
	Hash string
}

// Log records nothing in this build.
type Log struct{}

var errCompiledOut = errors.New("audit log compiled out by the no_audit build tag")

// New returns a nil Log.
func New(io.Writer, Anchor, func(error)) *Log { return nil }

// Anchor is the zero Anchor.
func (*Log) Anchor() Anchor { return Anchor{} }

// Record does nothing.
func (*Log) Record(Event) {}

// Files reports that auditing is compiled out.
func Files(string) ([]string, error) { return nil, errCompiledOut }

// VerifyFiles reports that auditing is compiled out.
func VerifyFiles([]string) (int, error) { return 0, errCompiledOut }

// Resume reports the zero Anchor.
func Resume(string) (Anchor, error) { return Anchor{}, nil }
