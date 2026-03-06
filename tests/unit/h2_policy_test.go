//go:build no_h2

package unit

import (
	"net/http"
	"testing"

	"github.com/keelcore/keel/pkg/core/httpx"
)

func TestApplyHTTP2Policy_DisablesH2(t *testing.T) {
	srv := &http.Server{}
	httpx.ApplyHTTP2Policy(srv)
	if srv.TLSNextProto == nil {
		t.Fatalf("expected TLSNextProto to be set (empty map), got nil")
	}
	if len(srv.TLSNextProto) != 0 {
		t.Fatalf("expected empty TLSNextProto map, got %d entries", len(srv.TLSNextProto))
	}
}
