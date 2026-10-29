//go:build !no_statsd

package statsd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A client-chosen method must not reach the DogStatsD tag set verbatim: "|" and
// "#" are legal method characters and would inject tags; it becomes OTHER.
func TestInstrument_NonStandardMethodTaggedOther(t *testing.T) {
	c, pc := newLoopbackClient(t, "keel")
	h := Instrument(c, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET|#evil", "/", nil))
	joined := readOne(t, pc) + "\n" + readOne(t, pc)
	if !strings.Contains(joined, "keel.requests_total:1|c|#method:OTHER,status:200") {
		t.Fatalf("expected OTHER method tag, got %q", joined)
	}
	if strings.Contains(joined, "evil") {
		t.Errorf("raw method leaked into tags: %q", joined)
	}
}
