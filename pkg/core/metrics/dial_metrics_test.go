//go:build !no_prom

package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpstreamDialCounters(t *testing.T) {
	m := New()
	m.IncUpstreamDialRetry()
	m.IncUpstreamDialRetry()
	m.IncUpstreamDialFailure()

	var buf bytes.Buffer
	m.writeTo(&buf)
	out := buf.String()
	for _, want := range []string{
		"# TYPE keel_upstream_dial_retries_total counter",
		"keel_upstream_dial_retries_total 2",
		"# TYPE keel_upstream_dial_failures_total counter",
		"keel_upstream_dial_failures_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q in:\n%s", want, out)
		}
	}
}
