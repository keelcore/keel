package protonotice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// U5: NoticePath is the path the rejection points at.
func TestNoticePath(t *testing.T) {
	if NoticePath != "/.well-known/keel/upgrade" {
		t.Fatalf("NoticePath = %q", NoticePath)
	}
}

// A caller can name the requirement (e.g. HTTP/3 instead of the default HTTP/2).
func TestWriteRejection_RequiredOverride(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRejection(rec, httptest.NewRequest(http.MethodGet, "/", nil), HTTPVersionBelowMin, "http://h:8080", "HTTP/3")
	if req, _ := decodeProblem(t, rec.Body.Bytes())["required"].([]any); len(req) != 1 || req[0] != "HTTP/3" {
		t.Fatalf("required = %v, want [HTTP/3]", req)
	}
}
