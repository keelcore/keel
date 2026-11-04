package protonotice

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The catalog is a stable contract for automation: a code never changes meaning.
// This table pins every code's reason, status and default requirement.
func TestCatalog_CodesNeverChangeMeaning(t *testing.T) {
	for _, tc := range []struct {
		code     Code
		reason   string
		status   int
		required []string
	}{
		{TLSVersion, "tls_version_below_1_3", http.StatusMovedPermanently, []string{"TLS 1.3"}},
		{NoSharedALPN, "no_shared_alpn", 0, []string{"h2", "http/1.1"}},
		{NoSharedGroup, "no_shared_group", 0, []string{"P-256", "P-384"}},
		{NoSharedCipher, "no_shared_cipher", 0, []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"}},
		{ClientCertRequired, "client_certificate_required", http.StatusMovedPermanently, []string{"client certificate"}},
		{PlaintextOnHTTPS, "plaintext_on_https_port", http.StatusMovedPermanently, []string{"https"}},
		{HTTPVersionBelowMin, "http_version_below_minimum", http.StatusMovedPermanently, []string{"HTTP/2"}},
	} {
		e, ok := Lookup(tc.code)
		if !ok {
			t.Fatalf("%s: not in the catalog", tc.code)
		}
		if e.Reason != tc.reason || e.Status != tc.status || !slices.Equal(e.Required, tc.required) {
			t.Errorf("%s = {%s %d %v}, want {%s %d %v}", tc.code, e.Reason, e.Status, e.Required, tc.reason, tc.status, tc.required)
		}
		if e.Title == "" || e.Detail == "" {
			t.Errorf("%s: title and detail are required", tc.code)
		}
	}
	if len(Codes()) != 7 {
		t.Fatalf("catalog has %d codes, want 7; a new code needs a row above", len(Codes()))
	}
	if _, ok := Lookup("KEEL-PROTO-999"); ok {
		t.Fatal("unknown code found")
	}
}

func TestCatalog_CodeStrings(t *testing.T) {
	want := []Code{"KEEL-PROTO-001", "KEEL-PROTO-002", "KEEL-PROTO-003", "KEEL-PROTO-004",
		"KEEL-PROTO-005", "KEEL-PROTO-006", "KEEL-PROTO-007"}
	if got := Codes(); !slices.Equal(got, want) {
		t.Fatalf("Codes() = %v, want %v", got, want)
	}
}

// The rejection body is RFC 9457 problem+json with keel's code as an extension.
func TestProblem_IsRFC9457(t *testing.T) {
	e, _ := Lookup(TLSVersion)
	p := e.Problem("http://h:8080/.well-known/keel/upgrade?code=KEEL-PROTO-001", "01JD")
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"type":       "https://github.com/keelcore/keel/blob/main/docs/errors.md#keel-proto-001",
		"title":      e.Title,
		"status":     float64(http.StatusMovedPermanently),
		"detail":     e.Detail,
		"code":       "KEEL-PROTO-001",
		"reason":     "tls_version_below_1_3",
		"notice_url": "http://h:8080/.well-known/keel/upgrade?code=KEEL-PROTO-001",
		"request_id": "01JD",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if req, _ := got["required"].([]any); len(req) != 1 || req[0] != "TLS 1.3" {
		t.Errorf("required = %v, want [TLS 1.3]", got["required"])
	}
	if ContentType != "application/problem+json" {
		t.Fatalf("ContentType = %q", ContentType)
	}
}

// Optional members are omitted when empty.
func TestProblem_OmitsEmptyMembers(t *testing.T) {
	e, _ := Lookup(NoSharedALPN)
	b, err := json.Marshal(e.Problem("", ""))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"notice_url", "request_id", "status"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s present, want omitted", k)
		}
	}
}

// Messages name only the requirement, never keel internals.
func TestCatalog_MessagesNameOnlyTheRequirement(t *testing.T) {
	for _, c := range Codes() {
		e, _ := Lookup(c)
		for _, s := range []string{e.Title, e.Detail} {
			for _, leak := range []string{"keel", "Keel", "config", "go1", "version 0."} {
				if containsFold(s, leak) {
					t.Errorf("%s: %q mentions %q", c, s, leak)
				}
			}
		}
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
