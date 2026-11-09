package knobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Secret holds a secret value. Every way of printing or encoding it yields Redacted; Reveal is
// the one deliberate read.
type Secret struct{ v *string }

// NewSecret wraps a value.
func NewSecret(s string) Secret { return Secret{v: &s} }

// Reveal returns the value.
func (s Secret) Reveal() string {
	if s.v == nil {
		return ""
	}
	return *s.v
}

func (Secret) String() string   { return Redacted }
func (Secret) GoString() string { return Redacted }

// Format prints Redacted for every verb and flag.
func (Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, Redacted) }

func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal(Redacted) }

// UnmarshalJSON refuses: a secret enters a configuration only through its layers.
func (*Secret) UnmarshalJSON([]byte) error {
	return errors.New("knobs: a secret is not read from JSON")
}
