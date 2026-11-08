//go:build !no_otel

package tracing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keelcore/keel/pkg/config"
)

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
