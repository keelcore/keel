package probes

import (
	"net/http"
	"sync/atomic"
)

// Startup tracks whether the server has finished initializing.
// /startupz returns 503 until Done() is called.
type Startup struct {
	done atomic.Bool
}

func NewStartup() *Startup { return &Startup{} }

// Done marks initialization complete; subsequent /startupz calls return 200.
func (s *Startup) Done() { s.done.Store(true) }

// Get reports whether initialization is complete.
func (s *Startup) Get() bool { return s.done.Load() }

// RegisterStartup registers the /startupz handler on mux.
func RegisterStartup(mux *http.ServeMux, s *Startup) {
	mux.HandleFunc("/startupz", func(w http.ResponseWriter, _ *http.Request) {
		if s.Get() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("started\n"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("starting\n"))
	})
}
