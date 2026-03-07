package probes

import (
	"sync"
	"sync/atomic"
)

// namedCheck is a named readiness dependency function.
type namedCheck struct {
	name string
	fn   func() error
}

// Readiness tracks whether the server is ready to serve traffic.
// The atomic ready flag is the fast path used by the shedding middleware.
// Named checks (AddCheck) are additionally evaluated by the /readyz handler.
type Readiness struct {
	ready  atomic.Bool
	mu     sync.RWMutex
	checks []namedCheck
}

func NewReadiness() *Readiness {
	r := &Readiness{}
	r.ready.Store(true)
	return r
}

// Set updates the atomic ready flag (used by backpressure / health probe).
func (r *Readiness) Set(v bool) { r.ready.Store(v) }

// Get returns the atomic ready flag (fast path for shedding middleware).
func (r *Readiness) Get() bool { return r.ready.Load() }

// AddCheck registers a named dependency check. The function is called on each
// /readyz request; a non-nil error marks that check as failing.
func (r *Readiness) AddCheck(name string, fn func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, namedCheck{name: name, fn: fn})
}

// IsReady evaluates the atomic flag and all registered checks.
// Returns ok=true only when the flag is true AND every check passes.
// failing contains the names (and errors) of any failing checks.
func (r *Readiness) IsReady() (ok bool, failing []string) {
	if !r.ready.Load() {
		return false, []string{"backpressure"}
	}
	r.mu.RLock()
	checks := make([]namedCheck, len(r.checks))
	copy(checks, r.checks)
	r.mu.RUnlock()

	for _, c := range checks {
		if err := c.fn(); err != nil {
			failing = append(failing, c.name+": "+err.Error())
		}
	}
	return len(failing) == 0, failing
}