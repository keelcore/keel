// tests/unit/router_test.go
package unit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/router"
)

// SPEC: collisions are scoped to (port, pattern) only.
// Two ports may register identical patterns without conflict.
func TestRouter_SamePatternDifferentPorts_NoCollision(t *testing.T) {
	r := router.New()

	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("p8080")) }))
	r.Handle(8443, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("p8443")) }))

	h := r.Handler()

	reqA := httptest.NewRequest("GET", "http://example.com/x", nil)
	reqA.Host = "example.com:8080"
	rrA := httptest.NewRecorder()
	h.ServeHTTP(rrA, reqA)

	reqB := httptest.NewRequest("GET", "http://example.com/x", nil)
	reqB.Host = "example.com:8443"
	rrB := httptest.NewRecorder()
	h.ServeHTTP(rrB, reqB)

	a, _ := io.ReadAll(rrA.Result().Body)
	b, _ := io.ReadAll(rrB.Result().Body)

	if string(a) != "p8080" {
		t.Fatalf("expected p8080, got %q", string(a))
	}
	if string(b) != "p8443" {
		t.Fatalf("expected p8443, got %q", string(b))
	}
}

// SPEC: last-write-wins should not require Host to include a port in unit tests IF
// the request is already known to target exactly one port.
// We enforce determinism by always setting Host in tests.
func TestRouter_LastWriteWins_SamePort_Simple(t *testing.T) {
	r := router.New()

	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("first")) }))
	r.Handle(8080, "/x", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("second")) }))

	h := r.Handler()
	req := httptest.NewRequest("GET", "http://example.com/x", nil)
	req.Host = "example.com:8080"

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, string(body))
	}
	if !strings.Contains(string(body), "second") {
		t.Fatalf("expected last handler, got %q", string(body))
	}
}
