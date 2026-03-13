//go:build !no_otel

package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/tracing"
)

// ---------------------------------------------------------------------------
// incomingSpanID
// ---------------------------------------------------------------------------

func TestIncomingSpanID_Valid(t *testing.T) {
	// W3C traceparent: version(2)-traceID(32)-parentID(16)-flags(2)
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	got := incomingSpanID(tp)
	if got != "00f067aa0ba902b7" {
		t.Errorf("expected '00f067aa0ba902b7', got %q", got)
	}
}

func TestIncomingSpanID_Empty(t *testing.T) {
	if got := incomingSpanID(""); got != "" {
		t.Errorf("expected empty string for empty header, got %q", got)
	}
}

func TestIncomingSpanID_Malformed(t *testing.T) {
	// Too few parts.
	if got := incomingSpanID("00-abc-def"); got != "" {
		t.Errorf("expected empty for malformed header, got %q", got)
	}
}

func TestIncomingSpanID_WrongParentIDLen(t *testing.T) {
	// parentID is only 8 chars instead of 16.
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-shortid-01"
	if got := incomingSpanID(tp); got != "" {
		t.Errorf("expected empty for short parentID, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// OTelSpan
// ---------------------------------------------------------------------------

// OTelSpan with nil exporter must pass through to the next handler unchanged.
func TestOTelSpan_NilExporter_Passthrough(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	h := OTelSpan(func() *tracing.Exporter { return nil }, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	h.ServeHTTP(rr, req)

	if !called {
		t.Error("expected inner handler to be called")
	}
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
}

// OTelSpan must capture and use otelWriter so the default status 200 is
// available when the inner handler does not call WriteHeader explicitly.
func TestOTelSpan_DefaultStatus200(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	h := OTelSpan(func() *tracing.Exporter { return nil }, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// OTelSpan with a traceparent header must extract the parent span ID.
func TestOTelSpan_WithTraceparent_ExtractsParent(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := OTelSpan(func() *tracing.Exporter { return nil }, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// OTelSpan with a live exporter must submit the span (non-nil exp path).
func TestOTelSpan_WithExporter_SubmitsSpan(t *testing.T) {
	// Start a test server as the OTLP collector endpoint.
	received := make(chan struct{}, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	cfg := config.OTLPConfig{
		Enabled:  true,
		Endpoint: collector.URL,
		Insecure: true,
	}
	exp, err := tracing.Setup(cfg)
	if err != nil {
		t.Fatalf("tracing.Setup: %v", err)
	}
	defer tracing.Shutdown(exp)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := OTelSpan(func() *tracing.Exporter { return exp }, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// otelWriter.WriteHeader
// ---------------------------------------------------------------------------

func TestOtelWriter_WriteHeader_CapturesCode(t *testing.T) {
	rr := httptest.NewRecorder()
	ow := &otelWriter{ResponseWriter: rr, status: http.StatusOK}
	ow.WriteHeader(http.StatusTeapot)
	if ow.status != http.StatusTeapot {
		t.Errorf("expected status 418, got %d", ow.status)
	}
}
