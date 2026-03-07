//go:build no_prom

package metrics

import "net/http"

// Metrics is a no-op stub used when the no_prom build tag is active.
type Metrics struct{}

func New() *Metrics                                          { return &Metrics{} }
func (m *Metrics) Handler() http.Handler                     { return http.NotFoundHandler() }
func (m *Metrics) Instrument(next http.Handler) http.Handler { return next }
func (m *Metrics) Inflight() float64                         { return 0 }
func (m *Metrics) FIPSActive() float64                       { return 0 }
func (m *Metrics) SetCertExpiry(_ float64)                   {}
func (m *Metrics) SetLogDrops(_ int64)                       {}