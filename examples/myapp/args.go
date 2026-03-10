// examples/myapp/args.go
// CLI flag handling for myapp. Mirrors clisupport.ProcessArgs but loads
// the composed AppConfig rather than keel's default config.
package main

import (
	"github.com/keelcore/keel/pkg/clisupport"
	"github.com/keelcore/keel/pkg/core/logging"
)

func processArgs(log *logging.Logger) AppConfig {
	clisupport.TryVersion(log)
	cfg := loadConfig(log)
	clisupport.TryValidateApp(log)
	return cfg
}
