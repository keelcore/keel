package main

import (
	"context"
	"os"
	"time"

	"github.com/keelcore/keel/pkg/clisupport"
	"github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
)

func main() { os.Exit(run(nil)) }

// run contains the server startup logic. If log is nil, a default JSON logger
// is created. Returns 0 on normal exit; terminal flag paths (--version, etc.)
// exit via log.ExitFn which tests can intercept.
func run(log *logging.Logger) int {
	if log == nil {
		log = logging.New(logging.Config{JSON: true})
	}
	cfg := clisupport.ProcessArgs(log)
	srv := core.NewServer(
		log, cfg, core.WithDefaultRegistrar(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if d, err := time.ParseDuration(os.Getenv("KEEL_TEST_SHUTDOWN_AFTER")); err == nil && d > 0 {
		time.AfterFunc(d, cancel)
	}

	core.RunServer(srv, ctx)
	return 0
}
