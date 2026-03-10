// examples/myapp/main.go
// Demonstrates embedding keel as a library.
// Configuration: set APP_CONFIG and APP_SECRETS to YAML file paths.
package main

import (
	"context"
	"net/http"

	keelcore "github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/ports"
)

func main() {
	log := logging.New(logging.Config{JSON: true})
	cfg := processArgs(log)

	srv := keelcore.NewServer(log, cfg.Keel)
	srv.AddRoute(ports.HTTPS, "GET /hello", http.HandlerFunc(hello))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.Run(ctx)
}
