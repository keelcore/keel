//go:build !no_prom

package metrics

import (
	"bytes"
	"strings"
	"testing"
)

// U2: rejections, 301s sent and notice pages served are counted by code.
func TestProtocolRejectionCounters(t *testing.T) {
	m := New()
	m.IncTLSHandshakeRejection("KEEL-PROTO-001")
	m.IncTLSHandshakeRejection("KEEL-PROTO-001")
	m.IncTLSHandshakeRejection("KEEL-PROTO-003")
	m.IncProtocolRejection("KEEL-PROTO-006")
	m.IncProtocolNotice("KEEL-PROTO-006", "json")

	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	for _, want := range []string{
		"# TYPE keel_tls_handshake_rejections_total counter",
		`keel_tls_handshake_rejections_total{code="KEEL-PROTO-001"} 2`,
		`keel_tls_handshake_rejections_total{code="KEEL-PROTO-003"} 1`,
		"# TYPE keel_protocol_rejections_total counter",
		`keel_protocol_rejections_total{code="KEEL-PROTO-006"} 1`,
		"# TYPE keel_protocol_notices_served_total counter",
		`keel_protocol_notices_served_total{code="KEEL-PROTO-006",format="json"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
}
