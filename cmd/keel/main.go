package main

import (
    "context"
    "fmt"
    "os"

    "github.com/keelcore/keel/pkg/core"
)

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    cfg, err := core.LoadConfigFromEnvAndOptionalFile(os.Getenv("KEEL_CONFIG"))
    if err != nil {
        _, _ = fmt.Fprintf(os.Stderr, "config error: %v\n", err)
        os.Exit(2)
    }

    srv := core.NewServer(
        core.WithConfig(cfg),
        core.WithDefaultRegistrar(),
    )

    if err := srv.Run(ctx); err != nil {
        _, _ = fmt.Fprintf(os.Stderr, "server error: %v\n", err)
        os.Exit(1)
    }
}
