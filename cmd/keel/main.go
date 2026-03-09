package main

import (
	"context"

	"github.com/keelcore/keel/pkg/clisupport"
	"github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
)

// Each call below may terminate the process if its operation fails or a
// terminal flag (--version, --validate) is set. No error handling is required
// at this level; exit behaviour is encapsulated within the called function.
func main() {
	log := logging.New(logging.Config{JSON: true})

	clisupport.TryVersion()

	cfg := clisupport.TryValidateConfig(log)

	srv := core.NewServer(
		log, cfg, core.WithDefaultRegistrar(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clisupport.RunServer(srv, ctx)
}
