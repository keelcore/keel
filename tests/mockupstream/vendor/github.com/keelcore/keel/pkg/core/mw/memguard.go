package mw

import (
	"context"
	"math"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

// MemoryGuard sheds requests while memory headroom is below the configured
// floor (backpressure.min_headroom_bytes), so the process answers 503 instead
// of being OOM-killed or driven into swap.
type MemoryGuard struct{ low atomic.Bool }

// NewMemoryGuard returns a guard that is not shedding.
func NewMemoryGuard() *MemoryGuard { return &MemoryGuard{} }

// Low reports whether requests are currently shed.
func (g *MemoryGuard) Low() bool { return g.low.Load() }

// ShedOnLowMemory answers 503 (Retry-After: 1) while g reports low headroom.
func ShedOnLowMemory(g *MemoryGuard, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.Low() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server memory pressure", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// memLimit is a seam over the effective Go memory limit: GOMEMLIMIT,
// backpressure.heap_max_bytes, or the cgroup-derived limit set at startup.
var memLimit = func() int64 { return debug.SetMemoryLimit(-1) }

const memoryGuardInterval = 250 * time.Millisecond

// RunMemoryGuard samples headroom = effective memory limit − memory Go holds
// (Sys − HeapReleased, the quantity the GC memory limit governs; the cgroup
// limit is RAM, so this is headroom before swap or OOM). Below minHeadroom it
// sheds; it recovers at 2×minHeadroom (hysteresis). With no limit known, or
// minHeadroom ≤ 0, it never sheds. On exit the guard is cleared.
func RunMemoryGuard(ctx context.Context, g *MemoryGuard, minHeadroom int64, log *logging.Logger) {
	defer g.low.Store(false)
	if minHeadroom <= 0 {
		return
	}
	t := time.NewTicker(memoryGuardInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.sample(minHeadroom, log)
		}
	}
}

func (g *MemoryGuard) sample(minHeadroom int64, log *logging.Logger) {
	limit := memLimit()
	if limit == math.MaxInt64 {
		g.low.Store(false) // no limit known: nothing to measure against
		return
	}
	var ms runtime.MemStats
	readMemStats(&ms)
	headroom := limit - int64(ms.Sys-ms.HeapReleased)
	switch {
	case !g.Low() && headroom < minHeadroom:
		g.low.Store(true)
		log.Warn("memory_headroom_low", map[string]any{"headroom": headroom, "floor": minHeadroom, "limit": limit})
	case g.Low() && headroom >= 2*minHeadroom:
		g.low.Store(false)
		log.Info("memory_headroom_recovered", map[string]any{"headroom": headroom, "floor": minHeadroom, "limit": limit})
	}
}
