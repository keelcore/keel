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

// Hooks observe upstream retries; unused when the sidecar is compiled out.
type Hooks struct {
	Log        *logging.Logger
	DialRetry  func()
	DialFailed func()
	RetryAfter func()
}

func New(_ config.Config, _ ...func(*http.Request) error) (http.Handler, error) {
	return nil, errors.New("sidecar disabled by build tag")
}

func NewWithHooks(_ config.Config, _ Hooks, _ ...func(*http.Request) error) (http.Handler, error) {
	return nil, errors.New("sidecar disabled by build tag")
}

func StartHealthProbe(_ context.Context, _ config.SidecarConfig, _ http.RoundTripper, _ *probes.Readiness, _ *logging.Logger) {
}
