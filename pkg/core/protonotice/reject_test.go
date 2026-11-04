package protonotice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/ctxkeys"
)

func decodeProblem(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("body is not JSON: %v: %q", err, b)
	}
	return p
}

// U4: an in-protocol rejection is a 301 to the notice page that also carries the
// machine-readable problem, so automation that does not follow redirects gets it.
func TestWriteRejection_RedirectsToTheNotice(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.RequestID, "01JD"))
	rec := httptest.NewRecorder()
	WriteRejection(rec, r, HTTPVersionBelowMin, "http://h:8080")

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, want 301", rec.Code)
	}
	for k, want := range map[string]string{
		"Location":        "http://h:8080/.well-known/keel/upgrade?code=KEEL-PROTO-007",
		"Cache-Control":   "no-store",
		"Connection":      "close",
		"Content-Type":    ContentType,
		"Keel-Error-Code": "KEEL-PROTO-007",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	p := decodeProblem(t, rec.Body.Bytes())
	if p["code"] != "KEEL-PROTO-007" || p["notice_url"] != "http://h:8080/.well-known/keel/upgrade?code=KEEL-PROTO-007" || p["request_id"] != "01JD" {
		t.Errorf("problem = %v", p)
	}
}

// U4: with no reachable notice page, the problem is answered in place with 426.
func TestWriteRejection_NoNoticeIs426(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRejection(rec, httptest.NewRequest(http.MethodGet, "/", nil), HTTPVersionBelowMin, "")
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status %d, want 426", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("Location %q, want none", rec.Header().Get("Location"))
	}
	if p := decodeProblem(t, rec.Body.Bytes()); p["status"] != float64(http.StatusUpgradeRequired) || p["code"] != "KEEL-PROTO-007" {
		t.Errorf("problem = %v", p)
	}
}

// U6: the same rejection written as raw HTTP/1.1 onto a connection that never became HTTPS.
func TestWriteRawRejection(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRawRejection(&buf, PlaintextOnHTTPS, "http://h:8080", ""); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&buf), nil)
	if err != nil {
		t.Fatalf("not an HTTP response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Keel-Error-Code") != "KEEL-PROTO-006" ||
		resp.Header.Get("Location") != "http://h:8080/.well-known/keel/upgrade?code=KEEL-PROTO-006" || !resp.Close {
		t.Fatalf("got %d %v", resp.StatusCode, resp.Header)
	}
	var p map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p["code"] != "KEEL-PROTO-006" {
		t.Fatalf("problem = %v, %v", p, err)
	}
}

func notice(t *testing.T, target, accept string) (*httptest.ResponseRecorder, []string) {
	t.Helper()
	var served []string
	h := NoticeHandler(NoticeOptions{HelpURL: "https://help.example/upgrade?a=1&b=2", Contact: "ops@example.com"},
		func(c Code, format string) { served = append(served, string(c)+" "+format) })
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec, served
}

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

// U5: NoticePath is the path the rejection points at.
func TestNoticePath(t *testing.T) {
	if NoticePath != "/.well-known/keel/upgrade" {
		t.Fatalf("NoticePath = %q", NoticePath)
	}
}
