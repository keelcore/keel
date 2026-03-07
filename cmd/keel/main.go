package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/version"
)

func main() {
	var (
		configPath  = flag.String("config", os.Getenv("KEEL_CONFIG"), "path to keel.yaml config file")
		secretsPath = flag.String("secrets", os.Getenv("KEEL_SECRETS"), "path to keel-secrets.yaml secrets file")
		validate    = flag.Bool("validate", false, "validate config and exit without starting")
		showVersion = flag.Bool("version", false, "print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		info := version.Get()
		fmt.Printf("keel %s (commit=%s, built=%s, go=%s)\n",
			info.Version, info.Commit, info.BuildDate, info.GoVersion)
		if len(info.BuildTags) > 0 {
			fmt.Printf("tags: %s\n", strings.Join(info.BuildTags, ","))
		}
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath, *secretsPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}

	if err := config.Validate(cfg); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
		os.Exit(2)
	}

	if *validate {
		fmt.Println("config ok")
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := core.NewServer(
		core.WithConfig(cfg),
		core.WithConfigPaths(*configPath, *secretsPath),
		core.WithDefaultRegistrar(),
	)

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}