//go:build !no_prom

package metrics

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Arbitrary client-chosen methods must not mint new series: they collapse to a
// single method="OTHER" label (unbounded-cardinality memory DoS regression).
func TestInstrument_NonStandardMethodsCollapseToOther(t *testing.T) {
	m := New()
	h := m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	for _, method := range []string{"AAAA1", "AAAA2", "GET|#x"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/", nil))
	}
	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	if !strings.Contains(out, `method="OTHER",status="200"`) {
		t.Fatalf("expected OTHER label, got %q", out)
	}
	for _, leaked := range []string{"AAAA1", "AAAA2", "GET|#x"} {
		if strings.Contains(out, leaked) {
			t.Errorf("raw method %q leaked into metrics: %q", leaked, out)
		}
	}
}
