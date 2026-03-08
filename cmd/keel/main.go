package main

import (
	"context"

	"github.com/keelcore/keel/pkg/clisupport"
	keelconfig "github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core"
)

// Each call below may terminate the process if its operation fails or a
// terminal flag (--version, --validate) is set. No error handling is required
// at this level; exit behaviour is encapsulated within the called function.
func main() {
	clisupport.TryVersion()

	cfg := keelconfig.Default()
	clisupport.TryValidateConfig(cfg)

	srv := core.NewServer(
		core.WithConfig(cfg),
		core.WithDefaultRegistrar(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.Run(ctx)
}