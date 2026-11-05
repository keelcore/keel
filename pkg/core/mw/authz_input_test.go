//go:build !no_authz

package mw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Class D: one field line holding "ok,blocked" and two lines "ok" + "blocked" reach
// the policy as different inputs, so a policy can tell them apart.
func TestAuthzHeaders_FieldLinesStayDistinct(t *testing.T) {
	one := http.Header{}
	one.Add("X-Role", "ok,blocked")
	two := http.Header{}
	two.Add("X-Role", "ok")
	two.Add("X-Role", "blocked")

	a, err := json.Marshal(authzHeaders(one))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(authzHeaders(two))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatalf("policy input is identical for one and two field lines: %s", a)
	}
	blocked := func(h map[string][]string) bool {
		for _, v := range h["x-role"] {
			if v == "blocked" {
				return true
			}
		}
		return false
	}
	if blocked(authzHeaders(one)) || !blocked(authzHeaders(two)) {
		t.Fatal(`a policy denying the role "blocked" must allow "ok,blocked" and deny "ok" + "blocked"`)
	}
}

// HH6: the policy input carries a hostile header value as one JSON string.
func TestAuthzPayload_ClientValuesStayOneElement(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header["X-Hostile"] = []string{hostile}
	b, err := json.Marshal(authzPayload(req, "opa"))
	if err != nil {
		t.Fatal(err)
	}
	var in struct {
		Input struct {
			Headers map[string][]string `json:"headers"`
		} `json:"input"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	if v := in.Input.Headers["x-hostile"]; len(v) != 1 || v[0] != hostile {
		t.Fatalf("x-hostile = %q, want one element equal to the client value", v)
	}
}
