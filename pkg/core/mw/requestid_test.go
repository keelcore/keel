package mw

import (
	"testing"
)

// newULID returns a 26-character string every time.
func TestNewULID_Length26(t *testing.T) {
	id := newULID()
	if len(id) != 26 {
		t.Errorf("expected ULID length 26, got %d (%q)", len(id), id)
	}
}

// newULID returns different values on consecutive calls.
func TestNewULID_Uniqueness(t *testing.T) {
	a := newULID()
	b := newULID()
	if a == b {
		t.Error("expected different ULIDs on consecutive calls")
	}
}
