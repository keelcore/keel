// pkg/clisupport/actions.go
// Flag definitions and terminal actions for keel binaries.
package clisupport

import (
	"flag"
	"fmt"
	"os"
	"strings"

	keelconfig "github.com/keelcore/keel/pkg/config"
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

// TryValidateConfig exits with the config validation result if --validate was supplied.
func TryValidateConfig(cfg keelconfig.Config) {
	if !*flagValidate {
		return
	}
	if err := keelconfig.Validate(cfg); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
		os.Exit(2)
	}
	fmt.Println("config ok")
	os.Exit(0)
}