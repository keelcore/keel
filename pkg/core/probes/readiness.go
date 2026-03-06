package probes

import (
	"sync/atomic"
)

type Readiness struct {
	ready atomic.Bool
}

func NewReadiness() *Readiness {
	r := &Readiness{}
	r.ready.Store(true)
	return r
}

func (r *Readiness) Set(v bool) { r.ready.Store(v) }
func (r *Readiness) Get() bool  { return r.ready.Load() }
