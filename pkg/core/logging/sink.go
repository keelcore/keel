//go:build !no_remotelog

package logging

import (
	"bytes"
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// HTTPSink is an io.Writer that buffers log lines and POSTs them in batches
// to an HTTP endpoint. Writes are non-blocking: lines are dropped when the
// buffer is full and counted by DropsTotal.
type HTTPSink struct {
	endpoint      string
	buf           chan []byte
	drops         atomic.Int64
	flushInterval time.Duration
	client        *http.Client
}

// NewHTTPSink creates an HTTPSink. bufCap is the number of log lines to buffer;
// flushInterval controls how often buffered lines are sent even if not full.
func NewHTTPSink(endpoint string, bufCap int, flushInterval time.Duration) *HTTPSink {
	return &HTTPSink{
		endpoint:      endpoint,
		buf:           make(chan []byte, bufCap),
		flushInterval: flushInterval,
		client:        &http.Client{Timeout: 5 * time.Second},
	}
}

// Write buffers b for asynchronous delivery. It always returns (len(b), nil);
// if the buffer is full the line is silently dropped and DropsTotal incremented.
func (s *HTTPSink) Write(b []byte) (int, error) {
	line := make([]byte, len(b))
	copy(line, b)
	select {
	case s.buf <- line:
	default:
		s.drops.Add(1)
	}
	return len(b), nil
}

// DropsTotal returns the number of lines dropped due to buffer overflow.
func (s *HTTPSink) DropsTotal() int64 { return s.drops.Load() }

// Run processes buffered lines until ctx is cancelled, flushing on each
// flushInterval tick or when a batch of 100 lines accumulates. On
// cancellation the remaining buffer is flushed before returning.
func (s *HTTPSink) Run(ctx context.Context) {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	var batch [][]byte
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.post(batch)
		batch = batch[:0]
	}

	for {
		select {
		case line := <-s.buf:
			batch = append(batch, line)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			// Drain any remaining lines then do a final flush.
			for {
				select {
				case line := <-s.buf:
					batch = append(batch, line)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (s *HTTPSink) post(lines [][]byte) {
	body := bytes.Join(lines, nil)
	req, err := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/x-ndjson")
	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
