// pkg/clisupport/actions.go
// Flag definitions and terminal actions for keel binaries.
package clisupport

import (
	"flag"
	"os"
	"strings"

	keelconfig "github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/version"
)

var (
	flagVersion        = flag.Bool("version", false, "print version information and exit")
	flagValidate       = flag.Bool("validate", false, "validate config and exit without starting")
	flagCheckIntegrity = flag.Bool("check-integrity", false, "print license/trademark notice and exit")
	flagCheckShred     = flag.Bool("check-shred", false, "print machine-readable JSON security profile and exit")
)

func tryVersion(log *logging.Logger) {
	if !*flagVersion {
		return
	}
	info := version.Get()
	fields := map[string]any{
		"name":    "keel",
		"version": info.Version,
		"commit":  info.Commit,
		"built":   info.BuildDate,
		"go":      info.GoVersion,
	}
	if len(info.BuildTags) > 0 {
		fields["tags"] = strings.Join(info.BuildTags, ",")
	}
	log.Exit("version", fields)
}

func tryCheckIntegrity(log *logging.Logger) {
	if !*flagCheckIntegrity {
		return
	}
	log.Exit("integrity_ok", map[string]any{"license": licenseText, "trademark": trademarkText})
}

func tryCheckShred(log *logging.Logger) {
	if !*flagCheckShred {
		return
	}
	info := version.Get()

	tagSet := make(map[string]bool, len(info.BuildTags))
	for _, t := range info.BuildTags {
		tagSet[t] = true
	}

	var binSize int64
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(exe); err == nil {
			binSize = fi.Size()
		}
	}

	log.Exit("shred_ok", map[string]any{
		"fips_active":       tagSet["fips"] || os.Getenv("GOFIPS140") != "",
		"build_tags":        info.BuildTags,
		"binary_size_bytes": binSize,
		"acme":              !tagSet["no_acme"],
		"authn":             !tagSet["no_authn"],
		"authz":             !tagSet["no_authz"],
		"fips":              !tagSet["no_fips"],
		"h2":                !tagSet["no_h2"],
		"h3":                !tagSet["no_h3"],
		"otel":              !tagSet["no_otel"],
		"prom":              !tagSet["no_prom"],
		"remotelog":         !tagSet["no_remotelog"],
		"sidecar":           !tagSet["no_sidecar"],
		"statsd":            !tagSet["no_statsd"],
	})
}

func tryValidateConfig(log *logging.Logger) keelconfig.Config {
	cfg := keelconfig.Default(log)
	if err := keelconfig.Validate(cfg); err != nil {
		log.Fatal("config_invalid", map[string]any{"err": err.Error()})
	}
	if *flagValidate {
		log.Exit("config ok", nil)
	}
	return cfg
}

// TryVersion parses flags and handles all pre-config terminal actions.
// For library users who load their own config between flag checks.
func TryVersion(log *logging.Logger) {
	flag.Parse()
	tryVersion(log)
	tryCheckIntegrity(log)
	tryCheckShred(log)
}

// TryValidateApp exits cleanly if --validate was supplied.
// Call this after the application has validated its own config.
func TryValidateApp(log *logging.Logger) {
	if *flagValidate {
		log.Exit("config ok", nil)
	}
}

// ProcessArgs parses flags, handles all terminal actions, and returns the
// loaded keel config for normal startup. Many flags cause process exit.
func ProcessArgs(log *logging.Logger) keelconfig.Config {
	flag.Parse()
	tryVersion(log)
	tryCheckIntegrity(log)
	tryCheckShred(log)
	return tryValidateConfig(log)
}
