package mw

import (
	"math"
	"testing"
)

// Bytes held beyond math.MaxInt64 count as the most memory there can be, not as a negative amount.
func TestSample_HeldBeyondInt64IsLow(t *testing.T) {
	stubMemory(t, 1<<30, math.MaxUint64)
	g := NewMemoryGuard()
	g.sample(1<<20, guardLog())
	if !g.Low() {
		t.Fatal("held > MaxInt64: guard not low")
	}
}
