package unit

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
	"github.com/keelcore/keel/pkg/core/mw"
)

func TestAccessLog_WritesLogEntry(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{Out: &buf})
	h := mw.AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/foo", nil))
	out := buf.String()
	for _, want := range []string{"GET", "/foo", "418"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in log output, got:\n%s", want, out)
		}
	}
}

func TestAccessLog_CapturesBytesOut(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(logging.Config{Out: &buf})
	h := mw.AccessLog(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	out := buf.String()
	if !strings.Contains(out, "5") {
		t.Errorf("expected byte count 5 in log output, got:\n%s", out)
	}
}
