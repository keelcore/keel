package protonotice

import (
	"net/http"
	"strings"
	"testing"
)

// U5: the notice is negotiated: HTML for browsers, the problem JSON for automation, text otherwise.
func TestNoticeHandler_Formats(t *testing.T) {
	for _, tc := range []struct {
		target, accept, wantType, wantFormat string
	}{
		{"/.well-known/keel/upgrade?code=KEEL-PROTO-001", "text/html,application/xhtml+xml", "text/html; charset=utf-8", "html"},
		{"/.well-known/keel/upgrade?code=KEEL-PROTO-001", "application/json", ContentType, "json"},
		{"/.well-known/keel/upgrade?code=KEEL-PROTO-001", ContentType, ContentType, "json"},
		{"/.well-known/keel/upgrade?code=KEEL-PROTO-001&format=json", "text/html", ContentType, "json"},
		{"/.well-known/keel/upgrade?code=KEEL-PROTO-001", "*/*", "text/plain; charset=utf-8", "text"},
		{"/.well-known/keel/upgrade?code=KEEL-PROTO-001", "", "text/plain; charset=utf-8", "text"},
	} {
		rec, served := notice(t, tc.target, tc.accept)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != tc.wantType {
			t.Errorf("%s accept %q: %d %q, want 200 %q", tc.target, tc.accept, rec.Code, rec.Header().Get("Content-Type"), tc.wantType)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", tc.target, rec.Header().Get("Cache-Control"))
		}
		if !strings.Contains(rec.Body.String(), "TLS 1.3") {
			t.Errorf("%s accept %q: body lacks the requirement: %q", tc.target, tc.accept, rec.Body.String())
		}
		if len(served) != 1 || served[0] != "KEEL-PROTO-001 "+tc.wantFormat {
			t.Errorf("%s accept %q: served %v, want [KEEL-PROTO-001 %s]", tc.target, tc.accept, served, tc.wantFormat)
		}
	}
}

// U5: the page is derived from the code alone; nothing else in the request is reflected.
func TestNoticeHandler_ReflectsNothing(t *testing.T) {
	rec, _ := notice(t, "/.well-known/keel/upgrade?code=KEEL-PROTO-001&x=%3Cscript%3E", "text/html")
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("request input reflected into the page")
	}
	for _, target := range []string{"/.well-known/keel/upgrade?code=%3Cscript%3E", "/.well-known/keel/upgrade"} {
		rec, served := notice(t, target, "text/html")
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<script>") || len(served) != 0 {
			t.Errorf("%s: %d %q served %v, want 404 without reflection", target, rec.Code, rec.Body.String(), served)
		}
	}
}

// U5: operator strings appear escaped in the HTML page and as given in the others.
func TestNoticeHandler_OperatorStrings(t *testing.T) {
	rec, _ := notice(t, "/.well-known/keel/upgrade?code=KEEL-PROTO-005", "text/html")
	body := rec.Body.String()
	if !strings.Contains(body, `href="https://help.example/upgrade?a=1&amp;b=2"`) || !strings.Contains(body, "ops@example.com") {
		t.Errorf("HTML lacks help_url or contact: %q", body)
	}
	rec, _ = notice(t, "/.well-known/keel/upgrade?code=KEEL-PROTO-005", "")
	if !strings.Contains(rec.Body.String(), "https://help.example/upgrade?a=1&b=2") {
		t.Errorf("text lacks help_url: %q", rec.Body.String())
	}
}
