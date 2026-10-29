//go:build !no_h2

package httpx

import (
	"net/http"
	"testing"
)

// ApplyHTTP2Policy (!no_h2 build): default no-op leaves TLSNextProto nil.
func TestApplyHTTP2Policy_DefaultNoOp(t *testing.T) {
	srv := &http.Server{}
	ApplyHTTP2Policy(srv)
	// Default policy is a no-op; TLSNextProto must remain nil.
	if srv.TLSNextProto != nil {
		t.Errorf("expected TLSNextProto nil for default HTTP/2 policy, got %v", srv.TLSNextProto)
	}
}

// ALPN offers HTTP/2 first, with HTTP/1.1 as the fallback.
func TestNextProtos_OffersH2(t *testing.T) {
	got := NextProtos()
	if len(got) != 2 || got[0] != "h2" || got[1] != "http/1.1" {
		t.Errorf("NextProtos() = %v, want [h2 http/1.1]", got)
	}
}
