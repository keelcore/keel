//go:build !no_otel

package tracing

import (
	"testing"

	"github.com/keelcore/keel/pkg/config"
)

func TestSetup_Disabled_ReturnsNil(t *testing.T) {
	exp, err := Setup(config.OTLPConfig{Enabled: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp != nil {
		t.Fatal("expected nil Exporter when disabled")
	}
}

func TestSetup_EmptyEndpoint_ReturnsNil(t *testing.T) {
	exp, err := Setup(config.OTLPConfig{Enabled: true, Endpoint: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp != nil {
		t.Fatal("expected nil Exporter when endpoint is empty")
	}
}

func TestSetup_Enabled_ReturnsExporter(t *testing.T) {
	exp, err := Setup(config.OTLPConfig{
		Enabled:  true,
		Endpoint: "localhost:4318",
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil Exporter")
	}
	Shutdown(exp)
}

func TestShutdown_Nil_IsNoop(t *testing.T) {
	// Must not panic.
	Shutdown(nil)
}

func TestSubmit_DropsWhenFull(t *testing.T) {
	exp, err := Setup(config.OTLPConfig{
		Enabled:  true,
		Endpoint: "localhost:4318",
		Insecure: true,
	})
	if err != nil || exp == nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer Shutdown(exp)

	// Fill channel beyond capacity; Submit must not block.
	for range chanCap + 10 {
		exp.Submit(Span{TraceID: "a", SpanID: "b", Name: "test"})
	}
}
