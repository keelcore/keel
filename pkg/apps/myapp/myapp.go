// pkg/apps/myapp/myapp.go
// Package myapp demonstrates embedding keel as a library. examples/myapp/main.go
// is a thin one-line shell over Run.
// Configuration: set APP_CONFIG and APP_SECRETS to YAML file paths.
package myapp

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	keelcore "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/ports"

	"github.com/keelcore/keel/pkg/core/logging"
)

// Run wires the downstream app on top of the keel library: it loads the composed
// AppConfig, constructs a keel server, registers app routes on the HTTPS port,
// and runs until the process is signalled. It returns 0 on graceful shutdown so
// main can propagate it via os.Exit; process-fatal conditions (e.g. invalid
// config) exit non-zero internally via log.Fatal.
func Run() int {
	log := logging.New(logging.Config{JSON: true})
	cfg := processArgs(log)

	srv := keelcore.NewServer(log, cfg.Keel)
	srv.AddRoute(ports.HTTPS, "GET /hello", http.HandlerFunc(hello))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if d, err := time.ParseDuration(os.Getenv("KEEL_TEST_SHUTDOWN_AFTER")); err == nil && d > 0 {
		time.AfterFunc(d, cancel)
	}

	startDemoClientListener(ctx, srv, log)

	srv.Run(ctx)
	return 0
}

// startDemoClientListener registers an embedder-owned listener in keel's /readyz barrier via
// RegisterListener, configured by MYAPP_CLIENT_LISTENER_ADDR and MYAPP_CLIENT_LISTENER_DELAY
// ("never" holds the bind open). Inert when the addr is unset.
func startDemoClientListener(ctx context.Context, srv *keelcore.Server, log *logging.Logger) {
	addr := os.Getenv("MYAPP_CLIENT_LISTENER_ADDR")
	if addr == "" {
		return
	}
	onBound := srv.RegisterListener("myapp-client")
	delay := os.Getenv("MYAPP_CLIENT_LISTENER_DELAY")
	go func() {
		if delay == "never" {
			return
		}
		if d, err := time.ParseDuration(delay); err == nil && d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return
			}
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatal("client_listener_bind_failed", map[string]any{"addr": addr, "err": err.Error()})
			return
		}
		go func() { <-ctx.Done(); _ = ln.Close() }()
		onBound()
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("client ok\n"))
		}))
	}()
}
