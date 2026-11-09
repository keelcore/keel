package knobs

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// A JSON Schema validator ignores a keyword it does not recognize, so a misspelled rule
// ("patern") would go unenforced. knobs validates each schema document against its dialect's
// metaschema made strict: the metaschema's own keywords and the x-knobs- vocabulary are the only
// ones a schema object may hold ("unevaluatedProperties": false). The dialect's metaschema reaches
// every subschema through its dynamic reference, which resolves back to the strict one, so every
// subschema is held to it.

const strictURL = "urn:knobs:strict"

// strictSchemas are the strict metaschemas, compiled once each, of the dialects whose metaschemas
// reach subschemas through a dynamic reference; the anchor makes the strict one its target.
var strictSchemas = map[string]func() *jsonschema.Schema{
	"https://json-schema.org/draft/2020-12/schema": strictSchema("https://json-schema.org/draft/2020-12/schema",
		"$dynamicAnchor", "meta"),
	"https://json-schema.org/draft/2019-09/schema": strictSchema("https://json-schema.org/draft/2019-09/schema",
		"$recursiveAnchor", true),
}

func strictSchema(dialectURL, anchor string, value any) func() *jsonschema.Schema {
	return sync.OnceValue(func() *jsonschema.Schema {
		c := jsonschema.NewCompiler()
		c.UseLoader(jsonschema.SchemeURLLoader{})
		_ = c.AddResource(strictURL, map[string]any{ // fails only on a bad or repeated URL: this one is constant, the compiler new
			"$schema":               dialectURL,
			"$id":                   strictURL,
			"$ref":                  dialectURL,
			anchor:                  value,
			"patternProperties":     map[string]any{"^x-knobs-": true},
			"unevaluatedProperties": false,
		})
		return c.MustCompile(strictURL) // a constant schema: it compiles, or no schema loads
	})
}

// checkKeywords fails a schema document, already valid against its dialect's metaschema, that holds
// any keyword its dialect does not define, listing every one. The validator reports an
// unrecognized keyword where it is, and again, as unevaluated, each keyword of every schema object
// above it, up to the root; the keywords reported are the leaves: those with no unrecognized
// keyword deeper inside the object holding them.
func checkKeywords(doc map[string]any) error {
	dialectURL := dialect
	if s, ok := doc["$schema"].(string); ok {
		dialectURL = strings.TrimSuffix(s, "#")
	}
	strict, ok := strictSchemas[dialectURL]
	if !ok {
		return schemaError("", fmt.Sprintf("$schema %q: knobs reads draft 2019-09 and 2020-12, the drafts whose every keyword it can check", dialectURL))
	}
	var ve *jsonschema.ValidationError
	if !errors.As(strict().Validate(doc), &ve) {
		return nil
	}
	found := unevaluated(ve)
	parts := make([]string, len(found))
	for i, k := range found {
		parts[i] = fmt.Sprintf("%q at %s", k.Name, k.Location)
	}
	e := schemaError("", fmt.Sprintf("%d unrecognized keyword(s), in neither %s nor x-knobs-: %s",
		len(found), dialectURL, strings.Join(parts, ", ")))
	e.Keywords = found
	return e
}

// unevaluated lists the leaf unevaluated keywords of a validation error, in location order.
func unevaluated(ve *jsonschema.ValidationError) []Keyword {
	var all [][]string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if _, ok := e.ErrorKind.(*kind.FalseSchema); ok && e.SchemaURL == strictURL+"#/unevaluatedProperties" {
			all = append(all, e.InstanceLocation)
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	var out []Keyword
	for _, loc := range all {
		holder := loc[:len(loc)-1]
		if !deeperInside(all, holder) {
			out = append(out, Keyword{Name: loc[len(loc)-1], Location: pointer(holder)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Location < out[j].Location || out[i].Location == out[j].Location && out[i].Name < out[j].Name
	})
	return out
}

// deeperInside says some location lies inside a subschema of the object at holder.
func deeperInside(all [][]string, holder []string) bool {
	for _, loc := range all {
		if len(loc) > len(holder)+1 && equalPrefix(loc, holder) {
			return true
		}
	}
	return false
}

func equalPrefix(loc, prefix []string) bool {
	for i, tok := range prefix {
		if loc[i] != tok {
			return false
		}
	}
	return true
}

// pointer is a location as a JSON Pointer fragment, each token escaped (RFC 6901).
func pointer(tokens []string) string {
	var b strings.Builder
	b.WriteString("#")
	for _, tok := range tokens {
		b.WriteString("/")
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1"))
	}
	return b.String()
}
