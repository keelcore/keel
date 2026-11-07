//go:build !no_otel

package tracing

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

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
