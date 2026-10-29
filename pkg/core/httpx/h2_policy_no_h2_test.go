//go:build no_h2

package httpx

import (
	"net/http"
	"testing"
)

// no_h2: ALPN offers only HTTP/1.1 and the server has no h2 upgrade handler.
func TestH2Policy_NoH2(t *testing.T) {
	if got := NextProtos(); len(got) != 1 || got[0] != "http/1.1" {
		t.Errorf("NextProtos() = %v, want [http/1.1]", got)
	}
	srv := &http.Server{}
	ApplyHTTP2Policy(srv)
	if srv.TLSNextProto == nil || len(srv.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto = %v, want empty non-nil map", srv.TLSNextProto)
	}
}
