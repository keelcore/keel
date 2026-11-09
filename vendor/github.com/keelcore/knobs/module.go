package knobs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The value types a property may declare.
const (
	TypeString   = "string"
	TypeInteger  = "integer"
	TypeNumber   = "number"
	TypeBoolean  = "boolean"
	TypeArray    = "array"
	TypeObject   = "object"   // an object with no properties of its own: a mapping
	TypeAny      = "any"      // no single type
	TypeDuration = "duration" // a string with x-knobs-type: duration
)

// reservedModule holds the engine's own settings (the file-path knobs).
const reservedModule = "knobs"

const dialect = "https://json-schema.org/draft/2020-12/schema"

// schemaURL is where each schema document is registered with its own compiler.
const schemaURL = "urn:knobs:schema"

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	envPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// The x-knobs- vocabulary, by where each keyword may appear; any other x-knobs- keyword fails. Every
// other keyword is JSON Schema's, enforced by the validator.
var (
	documentKeywords = keywordSet("x-knobs-module", "x-knobs-owner")
	moduleKeywords   = keywordSet("x-knobs-owner")
	leafKeywords     = keywordSet("x-knobs-type", "x-knobs-secret", "x-knobs-connection", "x-knobs-env")
)

func keywordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// property is one knob: a leaf of a module schema, validated by its compiled schema.
type property struct {
	module, name string // name is the dotted path inside the module: port, http.port
	typ          string // one of the Type constants
	items        string // an array's item type, for converting text
	sch          *jsonschema.Schema
	def          any
	hasDefault   bool
	min, max     *float64 // minimum and maximum, for clamping connection values
	secret       bool
	connection   bool
	env          string
}

// module is one attached module: its knobs, its nested objects, its compiled schema, and the
// schema as written (for the master schema).
type module struct {
	name     string
	props    map[string]*property
	branches map[string]bool // the dotted paths of its nested objects; "" is the module
	sch      *jsonschema.Schema
	raw      map[string]any
	owner    string
	src      *source
}

// source is one attached Module: the modules its schema defines, its code values and its own files.
type source struct {
	modules              []string // in name order
	single               bool     // a module schema (x-knobs-module); else an application schema
	code                 map[string]any
	globalPath, userPath string
}

// schemaSet is every attached module of one application.
type schemaSet struct {
	app     string
	modules map[string]*module
	sources []*source
}

func (s *schemaSet) key(mod, name string) string { return joinKey(s.app, mod, name) }

// prop returns the property for module and name, or nil.
func (s *schemaSet) prop(mod, name string) *property {
	if m, ok := s.modules[mod]; ok {
		return m.props[name]
	}
	return nil
}

// sortedProps lists every property in key order.
func (s *schemaSet) sortedProps() []*property {
	var out []*property
	for _, m := range s.modules {
		for _, p := range m.props {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return s.key(out[i].module, out[i].name) < s.key(out[j].module, out[j].name)
	})
	return out
}

// joinKey joins the non-empty parts of a dotted key.
func joinKey(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ".")
}

func schemaError(key, msg string) *Error { return newError(CodeSchemaInvalid, "schema", key, msg) }

// parseSchema reads one schema document, JSON or YAML, and compiles it with the JSON Schema
// validator. With x-knobs-module it is one module schema; without, it is an application schema,
// each of whose properties is a module. It reports which.
func parseSchema(app string, data []byte) ([]*module, bool, error) {
	raw, err := decodeDocument(data)
	if err != nil {
		return nil, false, schemaError("", "not a JSON or YAML object: "+err.Error())
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(jsonschema.SchemeURLLoader{}) // no loader: a schema reaches no file or network
	sch, err := compileDocument(raw)
	if err != nil {
		return nil, false, schemaError("", err.Error())
	}
	if err := checkKeywords(raw); err != nil {
		return nil, false, err
	}
	w := &walker{app: app}
	if v, single := raw["x-knobs-module"]; single {
		name, _ := v.(string)
		if !usableModule(name) {
			return nil, false, schemaError("", fmt.Sprintf("x-knobs-module %q is not a usable module name", name))
		}
		m, err := w.module(name, raw, sch, documentKeywords)
		if err != nil {
			return nil, false, err
		}
		return []*module{m}, true, nil
	}
	mods, err := w.application(raw, sch)
	return mods, false, err
}

// compileDocument checks a schema document against its dialect's metaschema and compiles it, with
// its own compiler and no loader, so a $ref reaches no file and no network. Its compiled
// properties are the modules' and knobs' schemas.
func compileDocument(raw map[string]any) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(jsonschema.SchemeURLLoader{})
	_ = c.AddResource(schemaURL, raw) // fails only on a bad or repeated URL: this one is constant, the compiler new
	return c.Compile(schemaURL)
}

func usableModule(name string) bool { return namePattern.MatchString(name) && name != reservedModule }

// walker reads one compiled schema document into modules.
type walker struct{ app string }

func (w *walker) application(raw map[string]any, sch *jsonschema.Schema) ([]*module, error) {
	if err := shape(raw, w.app, moduleKeywords); err != nil {
		return nil, err
	}
	owner, err := readOwner(raw, w.app)
	if err != nil {
		return nil, err
	}
	props := raw["properties"].(map[string]any)
	if len(props) == 0 {
		return nil, schemaError(w.app, "an application schema defines at least one module")
	}
	var out []*module
	for _, name := range sortedKeys(props) {
		sub, ok := props[name].(map[string]any)
		if !ok || !usableModule(name) {
			return nil, schemaError(joinKey(w.app, name), "a module is an object property with a usable module name")
		}
		m, err := w.module(name, sub, sch.Properties[name], moduleKeywords)
		if err != nil {
			return nil, err
		}
		if m.owner == "" {
			m.owner = owner
		}
		out = append(out, m)
	}
	return out, nil
}

func (w *walker) module(name string, raw map[string]any, sch *jsonschema.Schema, allowed map[string]bool) (*module, error) {
	m := &module{name: name, props: map[string]*property{}, branches: map[string]bool{}, sch: sch, raw: raw}
	var err error
	if m.owner, err = readOwner(raw, joinKey(w.app, name)); err != nil {
		return nil, err
	}
	if err := w.branch(m, raw, sch, "", allowed); err != nil {
		return nil, err
	}
	return m, nil
}

// branch reads one object of a module: the module itself or a nested object. A property with
// properties of its own is a nested object; any other property is a knob.
func (w *walker) branch(m *module, raw map[string]any, sch *jsonschema.Schema, path string, allowed map[string]bool) error {
	key := joinKey(w.app, m.name, path)
	if err := shape(raw, key, allowed); err != nil {
		return err
	}
	m.branches[path] = true
	props := raw["properties"].(map[string]any)
	for _, name := range sortedKeys(props) {
		sub, subSch := joinKey(path, name), sch.Properties[name]
		child, ok := props[name].(map[string]any)
		if !ok || !namePattern.MatchString(name) {
			return schemaError(joinKey(key, name), "a property is an object with a lowercase snake case name")
		}
		if _, nested := child["properties"]; nested {
			if err := w.branch(m, child, subSch, sub, nil); err != nil {
				return err
			}
			continue
		}
		p, err := w.property(m.name, sub, child, subSch)
		if err != nil {
			return err
		}
		m.props[sub] = p
	}
	if list, ok := raw["required"].([]any); ok {
		for _, r := range list {
			if _, defined := props[r.(string)]; !defined {
				return schemaError(key, fmt.Sprintf("required names an unknown property %v", r))
			}
		}
	}
	return nil
}

// shape checks what knobs needs of an object beyond JSON Schema: it is closed, so a key no schema
// defines fails in every layer, and it uses only the x-knobs- keywords allowed there.
func shape(raw map[string]any, key string, allowed map[string]bool) error {
	if err := knobsKeywords(raw, key, allowed); err != nil {
		return err
	}
	if raw["type"] != "object" || raw["additionalProperties"] != false {
		return schemaError(key, `a module and each nested object is "type": "object" with "additionalProperties": false`)
	}
	if _, ok := raw["properties"].(map[string]any); !ok {
		return schemaError(key, "properties must be an object")
	}
	return nil
}

func knobsKeywords(raw map[string]any, key string, allowed map[string]bool) error {
	for _, k := range sortedKeys(raw) {
		if strings.HasPrefix(k, "x-knobs-") && !allowed[k] {
			return schemaError(key, "unsupported keyword "+k)
		}
	}
	return nil
}

func readOwner(raw map[string]any, key string) (string, error) {
	v, present := raw["x-knobs-owner"]
	if !present {
		return "", nil
	}
	if owner, _ := v.(string); owner != "" {
		return owner, nil
	}
	return "", schemaError(key, "x-knobs-owner must be a non-empty string")
}

func (w *walker) property(mod, name string, raw map[string]any, sch *jsonschema.Schema) (*property, error) {
	key := joinKey(w.app, mod, name)
	if err := knobsKeywords(raw, key, leafKeywords); err != nil {
		return nil, err
	}
	p := &property{module: mod, name: name, sch: sch}
	p.typ, p.items = typeOf(raw)
	steps := []func(*property, map[string]any) string{readKnobsType, readFlags, readBounds, readDefault}
	for _, step := range steps {
		if msg := step(p, raw); msg != "" {
			return nil, schemaError(key, msg)
		}
	}
	return p, nil
}

// typeOf is a knob's type and, for an array, its item type: a single JSON Schema type, else TypeAny.
func typeOf(raw map[string]any) (string, string) {
	t, _ := raw["type"].(string)
	switch t {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean, TypeObject:
		return t, ""
	case TypeArray:
		items, _ := raw["items"].(map[string]any)
		it, _ := typeOf(items)
		return TypeArray, it
	}
	return TypeAny, ""
}

func readKnobsType(p *property, raw map[string]any) string {
	kt, ok := raw["x-knobs-type"]
	if !ok {
		return ""
	}
	if kt != TypeDuration || p.typ != TypeString {
		return `x-knobs-type must be "duration", on a string`
	}
	p.typ = TypeDuration
	return ""
}

func readFlags(p *property, raw map[string]any) string {
	var ok bool
	for _, f := range []struct {
		word string
		dst  *bool
	}{{"x-knobs-secret", &p.secret}, {"x-knobs-connection", &p.connection}} {
		if v, present := raw[f.word]; present {
			if *f.dst, ok = v.(bool); !ok {
				return f.word + " must be a boolean"
			}
		}
	}
	if p.secret && p.connection {
		return "a secret is never connection-overridable"
	}
	if v, present := raw["x-knobs-env"]; present {
		if p.env, _ = v.(string); !envPattern.MatchString(p.env) {
			return "x-knobs-env must be an upper case environment variable name"
		}
	}
	return ""
}

// readBounds keeps minimum and maximum, which the validator enforces, to clamp connection values.
func readBounds(p *property, raw map[string]any) string {
	for _, b := range []struct {
		word string
		dst  **float64
	}{{"minimum", &p.min}, {"maximum", &p.max}} {
		if f, ok := asFloat(raw[b.word]); ok {
			*b.dst = &f
		}
	}
	return ""
}

func readDefault(p *property, raw map[string]any) string {
	v, present := raw["default"]
	if !present {
		return ""
	}
	if p.secret {
		return "a secret has no default"
	}
	val, code := coerceTyped(p, v)
	if code == "" {
		code = p.check(val)
	}
	if code != "" {
		return "default breaks its own rules: " + code
	}
	p.def, p.hasDefault = val, true
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// master is the master schema: the application object with one property per module, each the
// module schema as written without $schema and $id.
func (s *schemaSet) master() map[string]any {
	mods := map[string]any{}
	for name, m := range s.modules {
		cp := make(map[string]any, len(m.raw))
		for k, v := range m.raw {
			if k != "$schema" && k != "$id" {
				cp[k] = v
			}
		}
		if m.owner != "" {
			cp["x-knobs-owner"] = m.owner
		}
		if v := ownerVersion(m.owner); v != "" {
			cp["x-knobs-owner-version"] = v
		}
		mods[name] = cp
	}
	return map[string]any{
		"$schema":              dialect,
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			s.app: map[string]any{"type": "object", "additionalProperties": false, "properties": mods},
		},
	}
}
