// examples/myapp/handler_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHello_Returns200WithBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rr := httptest.NewRecorder()

	hello(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hello, from downstream app based on keel library") {
		t.Errorf("unexpected body: %q", rr.Body.String())
	}
	if ct := rr.Header().Get("content-type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("unexpected content-type: %q", ct)
	}
}
