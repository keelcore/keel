// examples/myapp/config.go
// App-level config wrapping the keel library config under the "keel:" key.
package main

import (
	"os"

	keelconfig "github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"gopkg.in/yaml.v3"
)

// AppConfig is the top-level config struct for myapp.
// The "keel" key maps directly to the keel library's Config type;
// absent keys in the YAML retain library defaults.
type AppConfig struct {
	App  AppSettings       `yaml:"app"`
	Keel keelconfig.Config `yaml:"keel"`
}

type AppSettings struct {
	Name string `yaml:"name"`
}

// loadConfig reads APP_CONFIG and APP_SECRETS, merges them onto library
// defaults, applies env-var overrides, validates, and returns the result.
// Calls log.Fatal on any error.
func loadConfig(log *logging.Logger) AppConfig {
	cfg := AppConfig{Keel: keelconfig.Defaults()}
	applyYAML(log, os.Getenv("APP_CONFIG"), &cfg)
	applyYAML(log, os.Getenv("APP_SECRETS"), &cfg)

	keel, err := keelconfig.From(&cfg.Keel)
	if err != nil {
		log.Fatal("config_invalid", map[string]any{"err": err.Error()})
	}
	cfg.Keel = keel
	return cfg
}

func applyYAML(log *logging.Logger, path string, dst any) {
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("config_read_failed", map[string]any{"path": path, "err": err.Error()})
	}
	if err := yaml.Unmarshal(b, dst); err != nil {
		log.Fatal("config_parse_failed", map[string]any{"path": path, "err": err.Error()})
	}
}
