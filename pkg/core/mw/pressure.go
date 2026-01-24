package mw

import (
    "context"
    "runtime"
    "time"

    "github.com/keelcore/keel/pkg/config"
    "github.com/keelcore/keel/pkg/core/logging"
    "github.com/keelcore/keel/pkg/core/probes"
)

func RunPressureLoop(ctx context.Context, r *probes.Readiness, cfg config.Config, log *logging.Logger) {
    if cfg.HeapMaxBytes <= 0 {
        return
    }
    high := clamp01(cfg.PressureHighWatermark)
    low := clamp01(cfg.PressureLowWatermark)
    if low > high {
        low = high
    }

    t := time.NewTicker(250 * time.Millisecond)
    defer t.Stop()

    var latched bool
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            var ms runtime.MemStats
            runtime.ReadMemStats(&ms)

            pressure := float64(ms.HeapAlloc) / float64(cfg.HeapMaxBytes)
            if !latched && pressure >= high {
                latched = true
                r.Set(false)
                log.Warn("pressure_high", map[string]any{"heap_alloc": ms.HeapAlloc, "heap_max": cfg.HeapMaxBytes, "p": pressure})
            }
            if latched && pressure <= low {
                latched = false
                r.Set(true)
                log.Info("pressure_recovered", map[string]any{"heap_alloc": ms.HeapAlloc, "heap_max": cfg.HeapMaxBytes, "p": pressure})
            }
        }
    }
}

func clamp01(v float64) float64 {
    if v < 0 {
        return 0
    }
    if v > 1 {
        return 1
    }
    return v
}
