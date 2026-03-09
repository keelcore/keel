//go:build !no_remotelog && !windows

package logging

import (
	"io"
	"log/syslog"
)

// NewSyslogSink dials a remote syslog endpoint over TCP and returns an
// io.Writer that formats lines as RFC 5424 syslog messages.
// Returns nil, err if the dial fails.
func NewSyslogSink(endpoint string) (io.Writer, error) {
	return syslog.Dial("tcp", endpoint, syslog.LOG_INFO|syslog.LOG_DAEMON, "keel")
}
