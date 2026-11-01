//go:build no_remotelog || windows

package logging

import (
	"context"
	"errors"
	"io"
	"time"
)

// HTTPSink is a no-op stub used when the no_remotelog build tag is active.
type HTTPSink struct{}

func NewHTTPSink(_ string, _ int, _ time.Duration) *HTTPSink { return &HTTPSink{} }
func (s *HTTPSink) Write(b []byte) (int, error)              { return len(b), nil }
func (s *HTTPSink) DropsTotal() int64                        { return 0 }
func (s *HTTPSink) Run(_ context.Context)                    {}

// NewSyslogSink is a no-op stub used when the no_remotelog build tag is active.
func NewSyslogSink(_ string) (io.Writer, error) {
	return nil, errors.New("syslog disabled by build tag")
}
