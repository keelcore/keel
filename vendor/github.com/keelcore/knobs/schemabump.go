package knobs

import (
	"errors"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Bump is the semver level a schema change calls for, by its field set: a removed field is
// breaking (major), an added field is new functionality (minor), no change is a patch. This is
// keel's release rule for its configuration schema, kept so every application on knobs versions
// its configuration surface the same way.
type Bump struct {
	Level   string
	Removed []string
	Added   []string
}

// Subject is the release tag subject for the bump.
func (b Bump) Subject() string {
	switch b.Level {
	case "major":
		return fmt.Sprintf("removed %d config field(s)", len(b.Removed))
	case "minor":
		return fmt.Sprintf("added %d config field(s)", len(b.Added))
	}
	return "internal improvements; no config surface changes"
}

// SchemaBump compares the field sets of two JSON Schemas (JSON or YAML). An empty old schema
// (none at the previous release) makes every field added.
func SchemaBump(old, current []byte) (Bump, error) {
	var before []string
	if len(old) > 0 {
		var err error
		if before, err = SchemaFields(old); err != nil {
			return Bump{}, fmt.Errorf("previous schema: %w", err)
		}
	}
	after, err := SchemaFields(current)
	if err != nil {
		return Bump{}, fmt.Errorf("current schema: %w", err)
	}
	b := Bump{Removed: missingFrom(before, after), Added: missingFrom(after, before), Level: "patch"}
	switch {
	case len(b.Removed) > 0:
		b.Level = "major"
	case len(b.Added) > 0:
		b.Level = "minor"
	}
	return b, nil
}

// missingFrom lists the fields of a that b lacks, sorted; nil when there are none.
func missingFrom(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, f := range b {
		in[f] = true
	}
	var out []string
	for _, f := range a {
		if !in[f] {
			out = append(out, f)
		}
	}
	return out
}

// SchemaFields lists a JSON Schema's fields as sorted dotted paths: every property, descending
// into a property that has properties of its own, which is not itself a field.
func SchemaFields(schema []byte) ([]string, error) {
	var doc any
	if err := yaml.Unmarshal(schema, &doc); err != nil {
		return nil, err
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("the schema is not a mapping")
	}
	fields := walkProperties(root, "")
	sort.Strings(fields)
	return fields, nil
}

func walkProperties(schema map[string]any, prefix string) []string {
	props, _ := schema["properties"].(map[string]any)
	var out []string
	for name, v := range props {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		child, isMap := v.(map[string]any)
		if _, nested := child["properties"]; isMap && nested {
			out = append(out, walkProperties(child, path)...)
			continue
		}
		out = append(out, path)
	}
	return out
}
