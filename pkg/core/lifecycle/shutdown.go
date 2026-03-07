package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/keelcore/keel/pkg/core/logging"
)

type Orchestrator struct {
	log *logging.Logger
	ch  chan os.Signal
}

func NewShutdownOrchestrator(log *logging.Logger) *Orchestrator {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	return &Orchestrator{log: log, ch: ch}
}

func (o *Orchestrator) WaitForStop(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case sig := <-o.ch:
		o.log.Warn("shutdown_signal", map[string]any{"sig": sig.String()})
		return context.Canceled
	}
}

// GracefulStop calls fn with a context bounded by timeout.
func (o *Orchestrator) GracefulStop(timeout time.Duration, fn func(context.Context) error) error {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx)
}
