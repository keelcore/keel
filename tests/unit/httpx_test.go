//go:build !no_h2

// tests/unit/httpx_gaps_test.go
package unit

import (
	"net/http"
	"testing"

	"github.com/keelcore/keel/pkg/core/httpx"
)

// ApplyHTTP2Policy (!no_h2 build): default no-op leaves TLSNextProto nil.
func TestApplyHTTP2Policy_DefaultNoOp(t *testing.T) {
	srv := &http.Server{}
	httpx.ApplyHTTP2Policy(srv)
	// Default policy is a no-op; TLSNextProto must remain nil.
	if srv.TLSNextProto != nil {
		t.Errorf("expected TLSNextProto nil for default HTTP/2 policy, got %v", srv.TLSNextProto)
	}
}
