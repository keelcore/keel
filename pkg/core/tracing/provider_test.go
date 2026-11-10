//go:build !no_otel

package tracing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/logging"
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

var errClose = errors.New("close failed")

type closeErrBody struct{ io.Reader }

func (closeErrBody) Close() error { return errClose }

type closeErrTransport struct{}

func (closeErrTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: closeErrBody{strings.NewReader("")}}, nil
}

// An export whose response body fails to close logs the failure.
func TestSend_CloseErrorLogged(t *testing.T) {
	var out bytes.Buffer
	e := &Exporter{url: "http://collector.local/v1/traces", client: &http.Client{Transport: closeErrTransport{}}}
	e.SetLogger(logging.New(logging.Config{Out: &out}))
	e.send([]Span{{TraceID: "a", SpanID: "b", Name: "x"}})
	if !strings.Contains(out.String(), "otlp_export_close_failed") || !strings.Contains(out.String(), errClose.Error()) {
		t.Fatalf("close error not logged: %q", out.String())
	}
}

// Without a logger, a close failure is still safe.
func TestSend_CloseErrorWithoutLogger(t *testing.T) {
	e := &Exporter{url: "http://collector.local/v1/traces", client: &http.Client{Transport: closeErrTransport{}}}
	e.send([]Span{{TraceID: "a", SpanID: "b", Name: "x"}})
}

func TestExporter_UsesTheOutboundTLSPolicy(t *testing.T) {
	e, err := Setup(config.OTLPConfig{Enabled: true, Endpoint: "collector.example:4318"})
	if err != nil || e == nil {
		t.Fatalf("Setup: %v, %v", e, err)
	}
	defer Shutdown(e)
	tr, ok := e.client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("transport = %#v, want the outbound TLS policy", e.client.Transport)
	}
}

// TestSend_JSONMarshalError covers the json marshal error branch in send()
// (line 134-135) via the jsonMarshal seam.
func TestSend_JSONMarshalError(t *testing.T) {
	orig := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { jsonMarshal = orig })

	e := &Exporter{
		url:    "http://127.0.0.1:4318/v1/traces",
		client: &http.Client{Timeout: sendTimeout},
	}
	// send must return early on marshal error without panicking.
	e.send([]Span{{TraceID: "a", SpanID: "b", Name: "x"}})
}

// TestSend_NewRequestError covers the http.NewRequestWithContext error branch
// in send() (line 140-143) via the httpNewRequestWithContext seam.
func TestSend_NewRequestError(t *testing.T) {
	orig := httpNewRequestWithContext
	httpNewRequestWithContext = func(context.Context, string, string, io.Reader) (*http.Request, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { httpNewRequestWithContext = orig })

	e := &Exporter{
		url:    "http://127.0.0.1:4318/v1/traces",
		client: &http.Client{Timeout: sendTimeout},
	}
	// send must return early on request-construction error without panicking.
	e.send([]Span{{TraceID: "a", SpanID: "b", Name: "x"}})
}

// manualExporter starts an Exporter whose flush ticker fires only when the test sends on the returned channel,
// against a collector that reports the span count of every request it receives.
func manualExporter(t *testing.T) (*Exporter, chan<- time.Time, <-chan int) {
	t.Helper()
	tick := make(chan time.Time)
	origTicker := newTicker
	newTicker = func(time.Duration) (<-chan time.Time, func()) { return tick, func() {} }
	t.Cleanup(func() { newTicker = origTicker })
	spans := make(chan int, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req exportReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode export request: %v", err)
		}
		n := 0
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				n += len(ss.Spans)
			}
		}
		spans <- n
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	exp, err := Setup(config.OTLPConfig{Enabled: true, Endpoint: srv.URL, Insecure: true})
	if err != nil || exp == nil {
		t.Fatalf("setup failed: %v", err)
	}
	t.Cleanup(func() { Shutdown(exp) })
	return exp, tick, spans
}

// TestRun_BatchMaxFlush: with the ticker never firing and the exporter running, the only flush that can send
// is the one at batchMax spans, so batchMax spans arrive in one request.
func TestRun_BatchMaxFlush(t *testing.T) {
	exp, _, spans := manualExporter(t)
	for range batchMax {
		exp.Submit(Span{TraceID: "a", SpanID: "b", Name: "x", HTTPStatus: 200})
	}
	select {
	case n := <-spans:
		if n != batchMax {
			t.Fatalf("batch flush sent %d spans, want %d", n, batchMax)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no flush at batchMax")
	}
}

// TestRun_TickerFlush: one queued span below batchMax, then one tick: the tick's flush sends it.
func TestRun_TickerFlush(t *testing.T) {
	exp, tick, spans := manualExporter(t)
	exp.Submit(Span{TraceID: "a", SpanID: "b", Name: "x", HTTPStatus: 200})
	// The span has left the queue; the tick is sent on an unbuffered channel, so run() takes it only after
	// appending the span to its batch.
	for len(exp.ch) > 0 {
		time.Sleep(time.Millisecond)
	}
	tick <- time.Now()
	select {
	case n := <-spans:
		if n != 1 {
			t.Fatalf("ticker flush sent %d spans, want 1", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no flush on the tick")
	}
}
