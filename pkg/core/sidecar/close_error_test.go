//go:build !no_sidecar

package sidecar

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

// closeErrBody is a response body whose Close fails.
type closeErrBody struct{ io.Reader }

func (closeErrBody) Close() error { return errClose }

type closeErrTransport struct{ status int }

func (t closeErrTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{}, Body: closeErrBody{strings.NewReader("ok")}}, nil
}

// A probe whose response body fails to close logs the failure; the status still decides health.
func TestDoProbe_CloseErrorLogged(t *testing.T) {
	var out bytes.Buffer
	h := &upstreamHealth{}
	client := &http.Client{Transport: closeErrTransport{status: http.StatusOK}}
	doProbe(client, "http://upstream.local/health", h, logging.New(logging.Config{Out: &out}))
	if !h.ok.Load() {
		t.Fatal("200 probe: upstream not healthy")
	}
	if !strings.Contains(out.String(), "upstream_health_probe_close_failed") || !strings.Contains(out.String(), errClose.Error()) {
		t.Fatalf("close error not logged: %q", out.String())
	}
}

// An oversized response whose body fails to close reports both errors, so ErrorHandler still answers 502.
func TestCapResponse_TooLargeCloseErrorReported(t *testing.T) {
	resp := &http.Response{ContentLength: 64, Body: closeErrBody{strings.NewReader("")}}
	got := capResponse(4)(resp)
	if !errors.Is(got, errResponseTooLarge) || !errors.Is(got, errClose) {
		t.Fatalf("capResponse = %v, want errResponseTooLarge and the close error", got)
	}
}
