package knobs

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Redacted replaces a secret value in every dump, audit record and printed form.
const Redacted = "<redacted>"

// Value is one resolved key: its value (int64, float64, bool, string, []any or map[string]any), the
// layer that set it, and whether it is secret.
type Value struct {
	Key    string
	Value  any
	Layer  string
	Secret bool
}

// Display is the value as it may be shown: Redacted when secret.
func (v Value) Display() any {
	if v.Secret {
		return Redacted
	}
	return v.Value
}

// Config is a resolved configuration. The process configuration holds every value; a
// connection's configuration holds only that connection's overrides over its process
// configuration, which it shares, so a connection costs memory in proportion to its overrides.
// A Config is never modified after it is returned, so any number of goroutines may read it.
type Config struct {
	set       *schemaSet
	values    map[string]Value
	process   *Config
	overrides []Value // sorted by key
}

// Get returns the value of a dotted key, and whether any layer set it.
func (c *Config) Get(key string) (Value, bool) {
	if i := sort.Search(len(c.overrides), func(i int) bool { return c.overrides[i].Key >= key }); i < len(c.overrides) &&
		c.overrides[i].Key == key {
		return c.overrides[i], true
	}
	if c.process != nil {
		return c.process.Get(key)
	}
	v, ok := c.values[key]
	return v, ok
}

// Values lists every set value in key order.
func (c *Config) Values() []Value {
	base := c
	if c.process != nil {
		base = c.process
	}
	out := make([]Value, 0, len(base.values))
	for k := range base.values {
		v, _ := c.Get(k)
		out = append(out, v)
	}
	for _, v := range c.overrides {
		if _, inBase := base.values[v.Key]; !inBase {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Dump is the configuration as a tree rooted at the application, each nested key under its
// objects, secrets as Redacted.
func (c *Config) Dump() map[string]any {
	mods := map[string]any{}
	for _, v := range c.Values() {
		place(mods, strings.SplitN(v.Key, ".", 2)[1], v.Display())
	}
	return map[string]any{c.set.app: mods}
}

// lookup returns a set, non-secret value of the given type for module and name.
func (c *Config) lookup(module, name string, types ...string) (any, error) {
	key := c.set.key(module, name)
	p := c.set.prop(module, name)
	v, ok := c.Get(key)
	if p == nil || !ok {
		return nil, fmt.Errorf("knobs: %s is not set", key)
	}
	if v.Secret {
		return nil, fmt.Errorf("knobs: %s is secret; read it with Secret", key)
	}
	for _, t := range types {
		if p.typ == t {
			return v.Value, nil
		}
	}
	return nil, fmt.Errorf("knobs: %s is %s, not %v", key, p.typ, types)
}

// Int returns an integer value.
func (c *Config) Int(module, name string) (int64, error) {
	v, err := c.lookup(module, name, TypeInteger)
	if err != nil {
		return 0, err
	}
	return v.(int64), nil
}

// Float returns a number value; an integer value converts.
func (c *Config) Float(module, name string) (float64, error) {
	v, err := c.lookup(module, name, TypeNumber, TypeInteger)
	if err != nil {
		return 0, err
	}
	f, _ := asFloat(v)
	return f, nil
}

// Bool returns a boolean value.
func (c *Config) Bool(module, name string) (bool, error) {
	v, err := c.lookup(module, name, TypeBoolean)
	if err != nil {
		return false, err
	}
	return v.(bool), nil
}

// String returns a string value; a duration value is returned as written.
func (c *Config) String(module, name string) (string, error) {
	v, err := c.lookup(module, name, TypeString, TypeDuration)
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Duration returns a duration value.
func (c *Config) Duration(module, name string) (time.Duration, error) {
	v, err := c.lookup(module, name, TypeDuration)
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(v.(string))
}

// List returns an array value, as a copy.
func (c *Config) List(module, name string) ([]any, error) {
	v, err := c.lookup(module, name, TypeArray)
	if err != nil {
		return nil, err
	}
	return slices.Clone(v.([]any)), nil
}

// Secret returns a secret value, wrapped so it never prints.
func (c *Config) Secret(module, name string) (Secret, error) {
	key := c.set.key(module, name)
	v, ok := c.Get(key)
	if !ok || !v.Secret {
		return Secret{}, fmt.Errorf("knobs: %s is not a set secret", key)
	}
	s, _ := v.Value.(string)
	if s == "" {
		s = fmt.Sprint(v.Value)
	}
	return NewSecret(s), nil
}
