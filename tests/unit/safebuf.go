package unit

import (
	"bytes"
	"sync"
)

// SafeBuf is a goroutine-safe bytes.Buffer used to capture log output from
// background goroutines without triggering the race detector.
type SafeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *SafeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *SafeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
