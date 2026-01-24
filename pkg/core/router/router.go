// pkg/core/router/router.go
package router

import (
    "net"
    "net/http"
    "strconv"
    "sync"
    "sync/atomic"

    "github.com/keelcore/keel/pkg/core/ports"
)

type Registrar interface {
    Register(*Router)
}

type portMux struct {
    mu   sync.Mutex
    regs map[string]http.Handler
    mux  atomic.Pointer[http.ServeMux]
}

type Router struct {
    mu    sync.RWMutex
    ports map[int]*portMux
}

func New() *Router {
    return &Router{ports: make(map[int]*portMux)}
}

func (r *Router) Has(port int, pattern string) bool {
    r.mu.RLock()
    pm := r.ports[port]
    r.mu.RUnlock()
    if pm == nil {
        return false
    }

    pm.mu.Lock()
    _, ok := pm.regs[pattern]
    pm.mu.Unlock()
    return ok
}

func (r *Router) Handle(port int, pattern string, h http.Handler) {
    pm := r.getOrCreatePortMux(port)

    // Copy-on-write update:
    // - Update registry under lock
    // - Build a brand new ServeMux from the registry
    // - Atomically swap it in (in-flight requests keep using the old mux)
    pm.mu.Lock()
    if pm.regs == nil {
        pm.regs = make(map[string]http.Handler)
    }
    pm.regs[pattern] = h

    m := http.NewServeMux()
    for p, hh := range pm.regs {
        m.Handle(p, hh)
    }
    pm.mu.Unlock()

    pm.mux.Store(m)
}

func (r *Router) Handler() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        port := requestPort(req)

        r.mu.RLock()
        pm := r.ports[port]
        r.mu.RUnlock()

        if pm == nil {
            http.NotFound(w, req)
            return
        }

        m := pm.mux.Load()
        if m == nil {
            http.NotFound(w, req)
            return
        }
        m.ServeHTTP(w, req)
    })
}

func (r *Router) getOrCreatePortMux(port int) *portMux {
    r.mu.RLock()
    pm := r.ports[port]
    r.mu.RUnlock()
    if pm != nil {
        return pm
    }

    r.mu.Lock()
    defer r.mu.Unlock()

    pm = r.ports[port]
    if pm != nil {
        return pm
    }

    pm = &portMux{
        regs: make(map[string]http.Handler),
    }
    // start with an empty mux so reads don't see nil after first creation
    pm.mux.Store(http.NewServeMux())
    r.ports[port] = pm
    return pm
}

func requestPort(req *http.Request) int {
    // Real server: listener port
    if v := req.Context().Value(http.LocalAddrContextKey); v != nil {
        if a, ok := v.(net.Addr); ok {
            if _, p, err := net.SplitHostPort(a.String()); err == nil {
                if n, err2 := strconv.Atoi(p); err2 == nil {
                    return n
                }
            }
        }
    }

    // Host header
    if req.Host != "" {
        if _, p, err := net.SplitHostPort(req.Host); err == nil {
            if n, err2 := strconv.Atoi(p); err2 == nil {
                return n
            }
        }
    }

    // Absolute URL host (httptest sometimes sets this)
    if req.URL != nil && req.URL.Host != "" {
        if _, p, err := net.SplitHostPort(req.URL.Host); err == nil {
            if n, err2 := strconv.Atoi(p); err2 == nil {
                return n
            }
        }
    }

    return ports.HTTP
}

type RegistrarFunc func(*Router)

func (f RegistrarFunc) Register(r *Router) { f(r) }

// DefaultRegistrar registers "/" on the fixed main HTTP port,
// but does NOT act as a catch-all for other paths.
func DefaultRegistrar() Registrar {
    return RegistrarFunc(func(r *Router) {
        r.Handle(ports.HTTP, "/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
            if req.URL == nil || req.URL.Path != "/" {
                http.NotFound(w, req)
                return
            }
            w.Header().Set("content-type", "text/plain; charset=utf-8")
            _, _ = w.Write([]byte("keel: ok\n"))
        }))
    })
}
