package probes

import (
	"errors"
	"testing"
)

func TestReadinessSetGet(t *testing.T) {
	r := NewReadiness()
	if !r.Get() {
		t.Fatalf("new readiness should default to ready")
	}
	r.Set(false)
	if r.Get() {
		t.Fatalf("readiness should report not-ready after Set(false)")
	}
	r.Set(true)
	if !r.Get() {
		t.Fatalf("readiness should report ready after Set(true)")
	}
}

func TestIsReadyBackpressure(t *testing.T) {
	r := NewReadiness()
	r.Set(false)
	ok, failing := r.IsReady()
	if ok {
		t.Fatalf("IsReady ok = true, want false under backpressure")
	}
	if len(failing) != 1 || failing[0] != "backpressure" {
		t.Fatalf("failing = %v, want [backpressure]", failing)
	}
}

func TestIsReadyChecksPassAndFail(t *testing.T) {
	r := NewReadiness()
	r.AddCheck("db", func() error { return nil })
	r.AddCheck("cache", func() error { return errors.New("down") })

	ok, failing := r.IsReady()
	if ok {
		t.Fatalf("IsReady ok = true, want false with a failing check")
	}
	if len(failing) != 1 || failing[0] != "cache: down" {
		t.Fatalf("failing = %v, want [cache: down]", failing)
	}
}

func TestIsReadyAllChecksPass(t *testing.T) {
	r := NewReadiness()
	r.AddCheck("db", func() error { return nil })

	ok, failing := r.IsReady()
	if !ok {
		t.Fatalf("IsReady ok = false, want true when all checks pass")
	}
	if len(failing) != 0 {
		t.Fatalf("failing = %v, want empty", failing)
	}
}
