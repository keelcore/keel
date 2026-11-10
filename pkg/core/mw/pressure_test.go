package mw

import (
	"context"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/probes"
	"github.com/keelcore/keel/tests/unit"
)

// RunPressureLoop latches readiness off under high heap pressure and recovers
// once pressure falls back below the low watermark. The readMemStats seam
// injects deterministic heap readings.
func TestRunPressureLoop_LatchAndRecover(t *testing.T) {
	var calls int32
	orig := readMemStats
	readMemStats = func(ms *runtime.MemStats) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			ms.HeapAlloc = 950 // pressure 0.95 >= high(0.9) → latch
		} else {
			ms.HeapAlloc = 100 // pressure 0.10 <= low(0.5) → recover
		}
	}
	defer func() { readMemStats = orig }()

	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	r.Set(true)
	cfg := config.Config{Backpressure: config.BackpressureConfig{
		HeapMaxBytes:  1000,
		HighWatermark: 0.9,
		LowWatermark:  0.5,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()

	// Wait for the latch (readiness → false), then for recovery (→ true).
	deadline := time.After(5 * time.Second)
	waitFor := func(want bool) {
		for {
			if r.Get() == want {
				return
			}
			select {
			case <-deadline:
				t.Fatalf("timed out waiting for readiness=%v", want)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitFor(false)
	waitFor(true)

	cancel()
	<-done
}

// RunPressureLoop clamps the low watermark to the high watermark when low > high.
func TestRunPressureLoop_LowAboveHigh_Clamped(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	cfg := config.Config{Backpressure: config.BackpressureConfig{
		HeapMaxBytes:  1 << 40,
		HighWatermark: 0.3,
		LowWatermark:  0.7, // low > high → clamped to high
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("RunPressureLoop did not exit after cancel")
	}
}

// RunPressureLoop exits immediately when HeapMaxBytes <= 0.
func TestRunPressureLoop_ZeroHeapMax_ExitsImmediately(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{HeapMaxBytes: 0},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()

	select {
	case <-done:
		// good: exited immediately
	case <-time.After(500 * time.Millisecond):
		t.Error("RunPressureLoop did not exit immediately with HeapMaxBytes=0")
	}
}

// RunPressureLoop exits when context is cancelled.
func TestRunPressureLoop_ContextCancel_Exits(t *testing.T) {
	log := logging.New(logging.Config{Out: io.Discard})
	r := probes.NewReadiness()
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40, // 1 TiB — effectively unreachable in tests
			HighWatermark: 0.90,
			LowWatermark:  0.70,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Error("RunPressureLoop did not exit after context cancel")
	}
}

func TestClamp01_BelowZero_ReturnsZero(t *testing.T) {
	if got := clamp01(-0.5); got != 0 {
		t.Errorf("clamp01(-0.5): expected 0, got %f", got)
	}
}

func TestClamp01_AboveOne_ReturnsOne(t *testing.T) {
	if got := clamp01(1.5); got != 1 {
		t.Errorf("clamp01(1.5): expected 1, got %f", got)
	}
}

func TestClamp01_InRange_ReturnsValue(t *testing.T) {
	if got := clamp01(0.5); got != 0.5 {
		t.Errorf("clamp01(0.5): expected 0.5, got %f", got)
	}
}

func TestPressureLoop_EarlyReturn_HeapMaxBytesZero(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{} // HeapMaxBytes defaults to 0

	done := make(chan struct{})
	go func() {
		RunPressureLoop(context.Background(), r, cfg, log)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("RunPressureLoop did not return early when HeapMaxBytes=0")
	}
}

func TestPressureLoop_ExitsOnContextCancel(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40, // 1 TiB — pressure near zero; covers in-range clamp01
			HighWatermark: 0.9,
			LowWatermark:  0.7,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunPressureLoop(ctx, r, cfg, log)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("RunPressureLoop did not exit on context cancel")
	}
}

// clamp01(v < 0): both watermarks below zero are clamped to 0.
func TestPressureLoop_ClampNegativeWatermarks(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40,
			HighWatermark: -0.1,
			LowWatermark:  -0.5,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the loop body runs; clamp happens before the select
	RunPressureLoop(ctx, r, cfg, log)
}

// clamp01(v > 1): both watermarks above 1 are clamped to 1.
func TestPressureLoop_ClampAboveOneWatermarks(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40,
			HighWatermark: 1.5,
			LowWatermark:  1.2,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RunPressureLoop(ctx, r, cfg, log)
}

// low > high after clamping: low is adjusted down to match high.
func TestPressureLoop_LowGreaterThanHighAdjusted(t *testing.T) {
	r := probes.NewReadiness()
	log := logging.New(logging.Config{Out: io.Discard})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1 << 40,
			HighWatermark: 0.3,
			LowWatermark:  0.8, // > high → adjusted to 0.3
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RunPressureLoop(ctx, r, cfg, log)
}

// High watermark trigger: HeapMaxBytes=1 forces pressure >> any threshold,
// exercises the t.C case and the latch=true branch.
func TestPressureLoop_HighWatermarkTrigger(t *testing.T) {
	r := probes.NewReadiness()
	sb := &unit.SafeBuf{}
	log := logging.New(logging.Config{Out: sb})
	cfg := config.Config{
		Backpressure: config.BackpressureConfig{
			HeapMaxBytes:  1,   // 1 byte; HeapAlloc >> 1 → pressure > any threshold
			HighWatermark: 0.0, // clamped to 0; any positive pressure triggers latch
			LowWatermark:  -1,  // clamped to 0
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunPressureLoop(ctx, r, cfg, log)

	// Poll until the pressure latch fires (readiness → false) or 5 s deadline.
	// Fixed sleeps are unreliable under -race on slow CI runners; polling is not.
	deadline := time.Now().Add(5 * time.Second)
	for r.Get() && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond) // let goroutine exit before reading sb

	if r.Get() {
		t.Error("expected readiness false after high-pressure latch")
	}
	if !strings.Contains(sb.String(), "pressure_high") {
		t.Errorf("expected pressure_high log entry, got: %s", sb.String())
	}
}
