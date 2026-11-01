package mw

import (
	"net/http"
	"sync/atomic"

	"github.com/keelcore/keel/pkg/config"
)

// ConcurrencyLimit caps in-flight requests at cfg.Limits.MaxConcurrent.
// When that limit is reached, up to cfg.Limits.QueueDepth additional requests
// wait for a free slot; requests beyond that are rejected with 429. A queued
// request whose context expires before a slot becomes available receives 503.
//
// If MaxConcurrent ≤ 0 the middleware is a no-op pass-through.
func ConcurrencyLimit(cfg config.Config, next http.Handler) http.Handler {
	max := cfg.Limits.MaxConcurrent
	if max <= 0 {
		return next
	}
	depth := cfg.Limits.QueueDepth

	// slots is a counting semaphore: tokens represent available slots.
	slots := make(chan struct{}, max)
	for i := 0; i < max; i++ {
		slots <- struct{}{}
	}

	// total counts every goroutine currently inside this middleware,
	// both those actively serving and those waiting in the queue.
	var total atomic.Int64

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := total.Add(1)
		if int(n) > max+depth {
			total.Add(-1)
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		defer total.Add(-1)

		select {
		case <-slots:
			// Slot acquired; proceed.
		case <-r.Context().Done():
			http.Error(w, "request timeout waiting in queue", http.StatusServiceUnavailable)
			return
		}
		defer func() { slots <- struct{}{} }()

		next.ServeHTTP(w, r)
	})
}
