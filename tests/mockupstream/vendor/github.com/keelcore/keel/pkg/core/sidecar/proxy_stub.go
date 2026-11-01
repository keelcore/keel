//go:build no_sidecar

package sidecar

import (
	"context"
	"errors"
	"net/http"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
)

func New(_ config.Config, _ ...func(*http.Request) error) (http.Handler, error) {
	return nil, errors.New("sidecar disabled by build tag")
}

func StartHealthProbe(_ context.Context, _ config.SidecarConfig, _ http.RoundTripper, _ *probes.Readiness, _ *logging.Logger) {
}
