//go:build !no_authz

package mw

// Fuzz targets for ext_authz: request headers are attacker-controlled, and the
// OPA response body is untrusted network input.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

func FuzzAuthzParseOPA(f *testing.F) {
	f.Add(200, `{"result":true}`)
	f.Add(200, `{"result":"true"}`)
	f.Add(200, `{"result":true`)
	f.Add(403, `{"result":true}`)
	log := logging.New(logging.Config{Out: io.Discard})
	f.Fuzz(func(t *testing.T, status int, body string) {
		resp := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
		if authzParseOPA(resp, log) && status != http.StatusOK {
			t.Fatalf("authzParseOPA allowed a non-200 response (status %d)", status)
		}
	})
}

func FuzzAuthzHeaders(f *testing.F) {
	f.Add("Authorization", "Bearer x", "X-Forwarded-For", "10.0.0.1")
	f.Add("", "", "", "")
	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string) {
		h := http.Header{}
		h.Add(k1, v1)
		h.Add(k2, v2)
		for k := range authzHeaders(h) {
			if k != strings.ToLower(k) {
				t.Fatalf("authzHeaders kept non-lowercase key %q", k)
			}
		}
	})
}
