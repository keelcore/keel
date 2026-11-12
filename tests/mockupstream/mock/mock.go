// tests/mockupstream/mock/mock.go
// Package mock is a test upstream for keel's sidecar that can misbehave the way
// legacy services do. It embeds keel, built from this tree, as a library:
//   - keel serves the control plane (HTTP :8080), /healthz and /readyz, and
//     logs listener_up;
//   - the upstream socket (MOCK_UPSTREAM_ADDR, default :9000) is a raw TCP
//     listener the mock owns, enrolled in keel's /readyz via RegisterListener.
//
// Control routes (keel HTTP listener, default :8080, moved by KEEL_LISTENERS_HTTP_PORT):
//
//	POST /mode/{mode}  switch behavior, clear captures, drop open connections
//	POST /down         close the upstream socket (dials are refused)
//	POST /up           re-open it
//	GET  /captures     every request received since the last mode switch
//
// Each upstream connection and state change is logged as JSON on stdout.
package mock

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"

	keelconfig "github.com/keelcore/keel/pkg/config"
	keelcore "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
)

// Test seams: package-level indirections defaulting to the real logger, socket
// and run loop, so the unit tests exercise Run and the upstream without binding
// a port (the logger's ExitFn turns Fatal into a recoverable panic).
var (
	newLogger = func() *logging.Logger { return logging.New(logging.Config{JSON: true}) }
	listen    = net.Listen
	runServer = (*keelcore.Server).Run
)

// Run starts keel and the upstream socket and blocks until signalled.
func Run() int {
	log := newLogger()
	cfg := keelconfig.Defaults()
	cfg.Authn.Enabled = false // control routes are test-only; nothing to authenticate
	cfg, err := keelconfig.From(&cfg)
	if err != nil {
		log.Fatal("config_invalid", map[string]any{"err": err.Error()})
	}

	addr := os.Getenv("MOCK_UPSTREAM_ADDR")
	if addr == "" {
		addr = ":9000"
	}
	u := &upstream{addr: addr, mode: modeOK, log: log, conns: map[net.Conn]struct{}{}}

	// Routes attach to the configured HTTP port (KEEL_LISTENERS_HTTP_PORT moves it).
	control := cfg.Listeners.HTTP.Port
	srv := keelcore.NewServer(log, cfg)
	srv.AddRoute(control, "POST /mode/{mode}", http.HandlerFunc(u.handleMode))
	srv.AddRoute(control, "POST /down", http.HandlerFunc(u.handleDown))
	srv.AddRoute(control, "POST /up", http.HandlerFunc(u.handleUp))
	srv.AddRoute(control, "GET /captures", http.HandlerFunc(u.handleCaptures))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onBound := srv.RegisterListener("upstream")
	if err := u.up(); err != nil {
		log.Fatal("mock_upstream_bind_failed", map[string]any{"addr": addr, "err": err.Error()})
	}
	onBound()
	go func() { <-ctx.Done(); u.down() }()

	runServer(srv, ctx)
	return 0
}

// upstream is the misbehaving socket and its captured traffic.
type upstream struct {
	addr string
	log  *logging.Logger

	mu       sync.Mutex
	mode     string
	ln       net.Listener
	conns    map[net.Conn]struct{}
	captures bytes.Buffer
	seen     map[string]bool // paths already answered in this mode (retry-after)
}

// firstRequest reports whether path is seen for the first time in this mode.
func (u *upstream) firstRequest(path string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.seen[path] {
		return false
	}
	if u.seen == nil {
		u.seen = map[string]bool{}
	}
	u.seen[path] = true
	return true
}

func (u *upstream) capture(b []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.captures.Write(b)
	u.captures.WriteString("\n--- end of request ---\n")
}

// up binds the upstream socket (idempotent) and starts accepting.
func (u *upstream) up() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ln != nil {
		return nil
	}
	ln, err := listen("tcp", u.addr)
	if err != nil {
		return err
	}
	u.ln = ln
	go u.accept(ln)
	u.log.Info("mock_upstream_up", map[string]any{"addr": ln.Addr().String(), "mode": u.mode})
	return nil
}

// down closes the upstream socket (idempotent); new dials are refused.
func (u *upstream) down() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ln == nil {
		return
	}
	_ = u.ln.Close()
	u.ln = nil
	u.dropConnsLocked()
	u.log.Info("mock_upstream_down", map[string]any{"addr": u.addr})
}

func (u *upstream) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			u.log.Warn("mock_accept_failed", map[string]any{"err": err.Error()})
			continue
		}
		mode := u.track(conn)
		u.log.Info("mock_conn", map[string]any{"mode": mode, "peer": conn.RemoteAddr().String()})
		go func() {
			serve(u, conn, mode)
			u.untrack(conn)
		}()
	}
}

func (u *upstream) handleMode(w http.ResponseWriter, r *http.Request) {
	mode := r.PathValue("mode")
	if _, ok := behaviors[mode]; !ok {
		http.Error(w, "unknown mode: "+mode, http.StatusBadRequest)
		return
	}
	u.mu.Lock()
	u.mode = mode
	u.captures.Reset()
	u.seen = nil
	u.dropConnsLocked()
	u.mu.Unlock()
	u.log.Info("mock_mode", map[string]any{"mode": mode})
	w.WriteHeader(http.StatusNoContent)
}

func (u *upstream) handleDown(w http.ResponseWriter, _ *http.Request) {
	u.down()
	w.WriteHeader(http.StatusNoContent)
}

func (u *upstream) handleUp(w http.ResponseWriter, _ *http.Request) {
	if err := u.up(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (u *upstream) handleCaptures(w http.ResponseWriter, _ *http.Request) {
	u.mu.Lock()
	b := bytes.Clone(u.captures.Bytes())
	u.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

// track registers an accepted connection and returns the mode it is served in.
func (u *upstream) track(c net.Conn) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.conns[c] = struct{}{}
	return u.mode
}

func (u *upstream) untrack(c net.Conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.conns, c)
}

// dropConnsLocked closes every open connection, so after a mode switch or
// down no client keeps talking to the previous behavior. Caller holds u.mu.
func (u *upstream) dropConnsLocked() {
	for c := range u.conns {
		_ = c.Close()
		delete(u.conns, c)
	}
}
