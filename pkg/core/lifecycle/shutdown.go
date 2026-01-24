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
    signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
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

func (o *Orchestrator) GracefulStop(fn func(context.Context) error) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    return fn(ctx)
}
