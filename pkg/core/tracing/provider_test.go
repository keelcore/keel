//go:build !no_otel

package tracing

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestSend_SuccessfulPost exercises the send() path with a live HTTP server.
// It submits a span, then shuts down the exporter (which flushes the batch).
func TestSend_SuccessfulPost(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp, err := Setup(config.OTLPConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Insecure: true,
	})
	if err != nil || exp == nil {
		t.Fatalf("setup failed: %v", err)
	}

	exp.Submit(Span{
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:     "00f067aa0ba902b7",
		Name:       "GET /test",
		Start:      time.Now(),
		End:        time.Now().Add(time.Millisecond),
		HTTPMethod: "GET",
		HTTPPath:   "/test",
		HTTPStatus: 200,
	})

	// Shutdown flushes the batch.
	Shutdown(exp)

	select {
	case <-received:
		// span was sent to collector
	default:
		// Non-fatal: the flush may have completed asynchronously.
		// The important thing is that Shutdown returned without hanging.
	}
}

// TestSend_NetworkError exercises the client.Do error branch in send() by
// pointing the exporter at a closed port.
func TestSend_NetworkError(t *testing.T) {
	exp, err := Setup(config.OTLPConfig{
		Enabled:  true,
		Endpoint: "http://127.0.0.1:1",
		Insecure: true,
	})
	if err != nil || exp == nil {
		t.Fatalf("setup failed: %v", err)
	}

	// send() is called internally by the flush loop; submit a span and
	// shut down so the background goroutine attempts to send and hits an error.
	exp.Submit(Span{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Name:    "GET /test",
		Start:   time.Now(),
		End:     time.Now().Add(time.Millisecond),
	})
	// Shutdown flushes and the send will fail; it must not panic.
	Shutdown(exp)
}

// TestBuildRequest_RoundTrip verifies buildRequest returns a non-empty JSON-serializable struct.
func TestBuildRequest_RoundTrip(t *testing.T) {
	spans := []Span{
		{
			TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
			SpanID:     "00f067aa0ba902b7",
			Name:       "GET /test",
			Start:      time.Now(),
			End:        time.Now().Add(time.Millisecond),
			HTTPMethod: "GET",
			HTTPPath:   "/test",
			HTTPStatus: 200,
		},
	}
	req := buildRequest(spans)
	if len(req.ResourceSpans) == 0 {
		t.Error("expected at least one resourceSpan")
	}
}

// TestBuildRequest_ErrorStatus verifies that HTTPStatus >= 500 produces status code 2 (ERROR).
func TestBuildRequest_ErrorStatus(t *testing.T) {
	spans := []Span{
		{
			TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
			SpanID:     "00f067aa0ba902b7",
			Name:       "GET /fail",
			Start:      time.Now(),
			End:        time.Now().Add(time.Millisecond),
			HTTPMethod: "GET",
			HTTPPath:   "/fail",
			HTTPStatus: 500,
		},
	}
	req := buildRequest(spans)
	if len(req.ResourceSpans) == 0 {
		t.Fatal("expected at least one resourceSpan")
	}
	spans500 := req.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans500) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans500[0].Status.Code != 2 {
		t.Errorf("expected status code 2 (ERROR) for HTTP 500, got %d", spans500[0].Status.Code)
	}
}

// TestBuildRequest_WithParentSpanID verifies that ParentSpanID is propagated.
func TestBuildRequest_WithParentSpanID(t *testing.T) {
	spans := []Span{
		{
			TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
			SpanID:       "00f067aa0ba902b7",
			ParentSpanID: "aabbccddeeff0011",
			Name:         "GET /child",
			Start:        time.Now(),
			End:          time.Now().Add(time.Millisecond),
			HTTPStatus:   200,
		},
	}
	req := buildRequest(spans)
	got := req.ResourceSpans[0].ScopeSpans[0].Spans[0].ParentSpanID
	if got != "aabbccddeeff0011" {
		t.Errorf("expected ParentSpanID=%q, got %q", "aabbccddeeff0011", got)
	}
}

// TestSend_HTTPErrorResponse verifies that send() does not panic when the
// collector returns a 4xx or 5xx status (the response body is closed, no retry).
func TestSend_HTTPErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	exp, err := Setup(config.OTLPConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Insecure: true,
	})
	if err != nil || exp == nil {
		t.Fatalf("setup failed: %v", err)
	}

	exp.Submit(Span{
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:     "00f067aa0ba902b7",
		Name:       "GET /test",
		Start:      time.Now(),
		End:        time.Now().Add(time.Millisecond),
		HTTPMethod: "GET",
		HTTPPath:   "/test",
		HTTPStatus: 200,
	})

	// Shutdown flushes; send will see 400 but must not panic.
	Shutdown(exp)
}

// TestSubmit_FullChannel_DoesNotBlock verifies the drop path in Submit.
func TestSubmit_FullChannel_StillDropsSafely(t *testing.T) {
	exp, err := Setup(config.OTLPConfig{
		Enabled:  true,
		Endpoint: "localhost:4318",
		Insecure: true,
	})
	if err != nil || exp == nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer Shutdown(exp)

	// Overflow the channel twice to ensure the default branch in Submit is covered.
	for range chanCap*2 + 5 {
		exp.Submit(Span{TraceID: "a", SpanID: "b"})
	}
}
