//go:build !no_sidecar

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keelcore/keel/pkg/core/sidecar"
)

func TestSidecar_ReverseProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("upstream"))
	}))
	defer up.Close()

	h, err := sidecar.ReverseProxy(up.URL)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() != "upstream" {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}
