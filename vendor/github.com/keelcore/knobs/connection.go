package knobs

// Connection is the caller's state for one connection: key-to-text values and whether the
// connection is authorized for overrides. The application maps whatever the caller sends
// (connection headers, call metadata) to it; knobs keeps no state between connections.
type Connection struct {
	Values     map[string]string
	Authorized bool
}

// Outcome is what happened to one connection value (spec/README.md, Connections).
type Outcome uint8

const (
	OutcomeApplied Outcome = iota
	OutcomeClamped
	OutcomeRejectedUnauthorized
	OutcomeRejectedNotOverridable
	OutcomeRejectedMalformed
)

func (o Outcome) String() string {
	switch o {
	case OutcomeApplied:
		return "applied"
	case OutcomeClamped:
		return "clamped"
	case OutcomeRejectedUnauthorized:
		return "rejected_unauthorized"
	case OutcomeRejectedNotOverridable:
		return "rejected_not_overridable"
	case OutcomeRejectedMalformed:
		return "rejected_malformed"
	}
	return "unknown"
}

// Attempt is one connection value's key and outcome, recorded whether or not it applied.
type Attempt struct {
	Key     string
	Outcome Outcome
}

// WithConnection is the configuration one connection sees: the process configuration with the
// connection's applied and clamped values over it, and every attempt in key order. The
// process configuration is unchanged; the result shares it and holds only the overrides.
func (c *Config) WithConnection(conn Connection) (*Config, []Attempt) {
	process := c
	if c.process != nil {
		process = c.process
	}
	out := &Config{set: c.set, process: process, overrides: make([]Value, 0, len(conn.Values))}
	attempts := make([]Attempt, 0, len(conn.Values))
	for _, key := range sortedKeys(conn.Values) {
		v, outcome := c.connectionValue(key, conn.Values[key], conn.Authorized)
		if outcome == OutcomeApplied || outcome == OutcomeClamped {
			out.overrides = append(out.overrides, Value{Key: key, Value: v, Layer: LayerConnection})
		}
		attempts = append(attempts, Attempt{Key: key, Outcome: outcome})
	}
	return out, attempts
}

// connectionValue checks one value in the order of spec/README.md, Connections.
func (c *Config) connectionValue(key, text string, authorized bool) (any, Outcome) {
	if !authorized {
		return nil, OutcomeRejectedUnauthorized
	}
	mod, name, known := (&Schemas{set: c.set}).Lookup(key)
	p := c.set.prop(mod, name)
	if !known || !p.connection || p.secret {
		return nil, OutcomeRejectedNotOverridable
	}
	v, code := coerceText(p, text)
	if code == "" {
		code = p.check(v)
	}
	switch code {
	case "":
		return v, OutcomeApplied
	case CodeOutOfRange:
		// Clamped to minimum or maximum, the value must pass every rule, or it is malformed
		// (an exclusive bound, multipleOf).
		if clamped, _ := clampNumber(p, v); p.check(clamped) == "" {
			return clamped, OutcomeClamped
		}
	}
	return nil, OutcomeRejectedMalformed
}
