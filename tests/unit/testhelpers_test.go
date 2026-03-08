// tests/unit/testhelpers_test.go
package unit

import (
	"bytes"
	"sync"
)

// safeBuf is a goroutine-safe bytes.Buffer used to capture log output from
// background goroutines without triggering the race detector.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
