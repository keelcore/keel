// pkg/clisupport/actions.go
// Flag definitions and terminal actions for keel binaries.
package clisupport

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	keelconfig "github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/version"
)

var (
	flagVersion  = flag.Bool("version", false, "print version information and exit")
	flagValidate = flag.Bool("validate", false, "validate config and exit without starting")
)

// TryVersion parses flags and exits if --version was supplied.
// Must be called before any other flag-dependent action.
func TryVersion() {
	flag.Parse()
	if !*flagVersion {
		return
	}
	info := version.Get()
	fmt.Printf("keel %s (commit=%s, built=%s, go=%s)\n",
		info.Version, info.Commit, info.BuildDate, info.GoVersion)
	if len(info.BuildTags) > 0 {
		fmt.Printf("tags: %s\n", strings.Join(info.BuildTags, ","))
	}
	os.Exit(0)
}

// TryValidateConfig loads config via Default, validates it, then exits if
// --validate was supplied. Returns the loaded config for normal startup.
func TryValidateConfig(log *logging.Logger) keelconfig.Config {
	cfg := keelconfig.Default(log)
	if err := keelconfig.Validate(cfg); err != nil {
		log.Fatal("config_invalid", map[string]any{"err": err.Error()})
	}
	if *flagValidate {
		fmt.Println("config ok")
		os.Exit(0)
	}
	return cfg
}

// RunServer runs srv until ctx is cancelled.
// Fatal errors are handled internally by the server via its logger.
func RunServer(srv *core.Server, ctx context.Context) {
	srv.Run(ctx)
}