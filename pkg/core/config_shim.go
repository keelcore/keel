package core

import "github.com/keelcore/keel/pkg/config"

// LoadConfigFromEnvAndOptionalFile is a convenience shim so callers can remain in core.
func LoadConfigFromEnvAndOptionalFile(path string) (config.Config, error) {
    return config.LoadFromEnvAndOptionalFile(path)
}
