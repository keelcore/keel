//go:build !no_sidecar

package sidecar

import (
	"context"
	"net"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

// Hooks observe upstream dial retries; every field is optional.
type Hooks struct {
	Log        *logging.Logger // upstream_dial_retry / upstream_dial_failed events
	DialRetry  func()          // a failed connect will be retried
	DialFailed func()          // every connect attempt for a request failed
}

// dialFunc has the shape of http.Transport.DialContext.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// retryDial retries a failed upstream connect up to attempts tries in total,
// waiting backoff before the second try and doubling it after each. A failed
// dial sent no request bytes, so retrying is safe for every method, and the
// round trip — with the circuit breaker above it — sees a single outcome.
// Retries stop as soon as ctx ends (client gone, or timeouts.read passed).
func retryDial(dial dialFunc, attempts int, backoff time.Duration, hooks Hooks) dialFunc {
	if attempts <= 1 {
		return dial
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		wait := backoff
		for attempt := 1; ; attempt++ {
			conn, err := dial(ctx, network, addr)
			if err == nil || ctx.Err() != nil {
				return conn, err
			}
			fields := map[string]any{"attempt": attempt, "attempts": attempts, "addr": addr, "err": err.Error()}
			if attempt == attempts {
				hooks.warn("upstream_dial_failed", fields)
				call(hooks.DialFailed)
				return nil, err
			}
			hooks.warn("upstream_dial_retry", fields)
			call(hooks.DialRetry)
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, err
			case <-t.C:
			}
			wait *= 2
		}
	}
}

// baseDial is the transport's own dialer, or a plain net.Dialer when unset.
func baseDial(d dialFunc) dialFunc {
	if d != nil {
		return d
	}
	var nd net.Dialer
	return nd.DialContext
}

func (h Hooks) warn(msg string, fields map[string]any) {
	if h.Log != nil {
		h.Log.Warn(msg, fields)
	}
}

func call(f func()) {
	if f != nil {
		f()
	}
}
