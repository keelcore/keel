package mw

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// hostile carries each log destination's delimiters: field and list separators,
// quotes, backslash, CR/LF, NUL and non-ASCII.
const hostile = "1.2.3.4, x;y=\"z\"\\\r\n{\"forged\":1}\x00é"

// HH6: a client X-Forwarded-For lands in the access log as one opaque value, on
// one line, in both log formats.
func TestAccessLog_ClientValuesStayOneField(t *testing.T) {
	for _, jsonMode := range []bool{true, false} {
		var buf bytes.Buffer
		log := logging.New(logging.Config{JSON: jsonMode, Out: &buf})
		req := httptest.NewRequest(http.MethodGet, "/p", nil)
		req.Header.Set("X-Forwarded-For", hostile)
		AccessLog(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)
		out := buf.String()
		if strings.Count(out, "\n") != 1 {
			t.Fatalf("json=%v: entry spans %d lines: %q", jsonMode, strings.Count(out, "\n"), out)
		}
		if !jsonMode {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(out), &entry); err != nil {
			t.Fatalf("json entry does not parse: %v: %q", err, out)
		}
		if entry["xff"] != hostile {
			t.Fatalf("xff = %q, want the client value intact", entry["xff"])
		}
		if _, forged := entry["forged"]; forged {
			t.Fatal("client text became a log field")
		}
	}
}
