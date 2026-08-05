//go:build !no_remotelog && !windows

package logging

import (
	"errors"
	"log/syslog"
	"testing"
)

func TestNewSyslogSinkDialError(t *testing.T) {
	orig := syslogDial
	syslogDial = func(_, _ string, _ syslog.Priority, _ string) (*syslog.Writer, error) {
		return nil, errors.New("dial failed")
	}
	t.Cleanup(func() { syslogDial = orig })

	if _, err := NewSyslogSink("192.0.2.1:514"); err == nil {
		t.Fatal("expected dial error")
	}
}

func TestNewSyslogSinkDialSuccess(t *testing.T) {
	orig := syslogDial
	// Return (nil, nil): exercises the success return path without a real
	// syslog daemon. NewSyslogSink returns whatever the seam yields.
	syslogDial = func(_, _ string, _ syslog.Priority, _ string) (*syslog.Writer, error) {
		return nil, nil
	}
	t.Cleanup(func() { syslogDial = orig })

	if _, err := NewSyslogSink("192.0.2.1:514"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
