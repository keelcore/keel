//go:build !no_sidecar

package sidecar

import (
	"sync"
	"time"
)

type breakerState int

const (
	breakerClosed   breakerState = iota
	breakerOpen                  // requests fast-fail with 502
	breakerHalfOpen              // one probe allowed; concurrent requests rejected
)

type breaker struct {
	mu           sync.Mutex
	state        breakerState
	failures     int
	threshold    int
	resetTimeout time.Duration
	openedAt     time.Time
}

func newBreaker(threshold int, resetTimeout time.Duration) *breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if resetTimeout <= 0 {
		resetTimeout = 30 * time.Second
	}
	return &breaker{threshold: threshold, resetTimeout: resetTimeout}
}

// Allow reports whether the request may proceed.
// The returned onResult callback (non-nil when allowed=true) must be called
// with the outcome. Callers must not call onResult when allowed=false.
func (b *breaker) Allow() (allowed bool, onResult func(success bool)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case breakerClosed:
		return true, b.recordResult

	case breakerOpen:
		if time.Since(b.openedAt) >= b.resetTimeout {
			b.state = breakerHalfOpen
			return true, b.recordProbeResult
		}
		return false, nil

	case breakerHalfOpen:
		// Reject concurrent requests while probe is in flight.
		return false, nil
	}
	return false, nil
}

func (b *breaker) recordResult(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.failures = 0
		return
	}
	b.failures++
	if b.failures >= b.threshold {
		b.state = breakerOpen
		b.openedAt = time.Now()
	}
}

func (b *breaker) recordProbeResult(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.state = breakerClosed
		b.failures = 0
	} else {
		b.state = breakerOpen
		b.openedAt = time.Now()
	}
}