package metrics

import "testing"

func TestBoolGauge(t *testing.T) {
	if boolGauge(true) != 1 || boolGauge(false) != 0 {
		t.Fatalf("boolGauge: true=%g false=%g", boolGauge(true), boolGauge(false))
	}
}
