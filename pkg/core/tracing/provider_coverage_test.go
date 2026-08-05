//go:build !no_otel

package tracing

import (
	"context"
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

// TestRun_BatchMaxFlush covers the len(batch) >= batchMax flush branch in
// run() (line 114-115) by submitting more than batchMax spans rapidly.
func TestRun_BatchMaxFlush(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	defer Shutdown(exp)

	for range batchMax + 32 {
		exp.Submit(Span{TraceID: "a", SpanID: "b", Name: "x", HTTPStatus: 200})
	}
	// Give the background goroutine time to drain and hit the batchMax flush.
	deadline := time.Now().Add(2 * time.Second)
	for len(exp.ch) > batchMax && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

// TestRun_TickerFlush covers the ticker.C flush branch in run() (line 117-118).
// It submits a single span and waits for the periodic flush interval to elapse.
func TestRun_TickerFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timer-based flush test in -short mode")
	}
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
	defer Shutdown(exp)

	exp.Submit(Span{TraceID: "a", SpanID: "b", Name: "x", HTTPStatus: 200})

	// Wait past flushInterval so the ticker fires and flushes the batch.
	select {
	case <-received:
	case <-time.After(flushInterval + 3*time.Second):
		t.Fatal("ticker-driven flush did not reach the collector")
	}
}
