package knobs

import (
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// codeRank orders the error codes a failure maps to: when one value fails several keywords, the
// first code here is the one reported.
var codeRank = []string{CodeInvalidType, CodeNotInEnum, CodeOutOfRange, CodeInvalidLength, CodeMissingRequired,
	CodeUnknownKey, CodeInvalidValue}

// failure is one failed keyword: where in the value it failed, and its error code.
type failure struct {
	loc  []string
	code string
}

// check validates a value against the property's compiled schema: the error code, or "".
func (p *property) check(v any) string {
	if err := p.sch.Validate(v); err != nil {
		return firstCode(failures(err))
	}
	return ""
}

func firstCode(fs []failure) string {
	best := len(codeRank) - 1
	for _, f := range fs {
		if r := slices.Index(codeRank, f.code); r >= 0 && r < best {
			best = r
		}
	}
	return codeRank[best]
}

// failures lists the keywords a validation error reports as failing. Schema.Validate returns only
// *jsonschema.ValidationError.
func failures(err error) []failure {
	ve := err.(*jsonschema.ValidationError)
	var out []failure
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if missing := missingKeys(e.ErrorKind); missing != nil {
			for _, name := range missing {
				out = append(out, failure{loc: append(slices.Clone(e.InstanceLocation), name), code: CodeMissingRequired})
			}
			return
		}
		if grouping(e.ErrorKind) && len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		out = append(out, failure{loc: e.InstanceLocation, code: kindCode(e.ErrorKind)})
	}
	walk(ve)
	return out
}

// missingKeys lists the keys a failed required, dependentRequired or dependencies keyword wants;
// each is its own failure, at its own key.
func missingKeys(k jsonschema.ErrorKind) []string {
	switch k := k.(type) {
	case *kind.Required:
		return k.Missing
	case *kind.DependentRequired:
		return k.Missing
	case *kind.Dependency:
		return k.Missing
	}
	return nil
}

// grouping says a failed keyword only gathers others that failed under it: its causes are the
// failures. Any other keyword with causes (anyOf, oneOf, not) is a failure of its own.
func grouping(k jsonschema.ErrorKind) bool {
	switch k.(type) {
	case *kind.Group, *kind.Schema, *kind.Reference, *kind.AllOf:
		return true
	}
	return false
}

// kindCode is the error code of one failed keyword.
func kindCode(k jsonschema.ErrorKind) string {
	switch k.(type) {
	case *kind.Type:
		return CodeInvalidType
	case *kind.Enum, *kind.Const:
		return CodeNotInEnum
	case *kind.Minimum, *kind.Maximum, *kind.ExclusiveMinimum, *kind.ExclusiveMaximum:
		return CodeOutOfRange
	case *kind.MinLength, *kind.MaxLength, *kind.MinItems, *kind.MaxItems, *kind.MinProperties, *kind.MaxProperties:
		return CodeInvalidLength
	case *kind.AdditionalProperties:
		return CodeUnknownKey
	}
	return CodeInvalidValue
}

// checkModules validates each module's resolved values, as one object, against the module's whole
// schema, so the rules that span keys (required, dependentRequired, if/then) hold. The first
// failure in key order is the error.
func checkModules(set *schemaSet, values map[string]Value) error {
	trees := map[string]map[string]any{}
	for name := range set.modules {
		trees[name] = map[string]any{}
	}
	for k, v := range values {
		parts := strings.SplitN(k, ".", 3)
		place(trees[parts[1]], parts[2], v.Value)
	}
	var first *Error
	for _, name := range sortedKeys(set.modules) {
		m := set.modules[name]
		err := m.sch.Validate(trees[name])
		if err == nil {
			continue
		}
		for _, f := range failures(err) {
			key := set.key(name, m.pathOf(f.loc))
			if first == nil || key < first.Key || key == first.Key && slices.Index(codeRank, f.code) < slices.Index(codeRank, first.Code) {
				first = keyError(set, f.code, "resolve", key, "")
			}
		}
	}
	if first == nil {
		return nil
	}
	if first.Code == CodeMissingRequired {
		first.Msg = "no layer sets it"
	}
	return first
}

// pathOf is the dotted path of the knob or nested object at an instance location: an array index
// or a mapping key inside a knob names the knob.
func (m *module) pathOf(loc []string) string {
	path := ""
	for _, tok := range loc {
		if path = joinKey(path, tok); m.props[path] != nil {
			break
		}
	}
	return path
}

// place sets a value at a dotted path in a tree of mappings, making the mappings on the way.
func place(tree map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := tree[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			tree[p] = next
		}
		tree = next
	}
	tree[parts[len(parts)-1]] = v
}
