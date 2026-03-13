// cmd/config-schema/main_test.go
package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/keelcore/keel/pkg/config"
)

// errReader always returns an error on Read, used to exercise decode-error paths.
type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }

// directUnmarshaler implements yaml.Unmarshaler directly (non-pointer).
type directUnmarshaler struct{}

func (directUnmarshaler) UnmarshalYAML(_ *yaml.Node) error { return nil }

// ---------------------------------------------------------------------------
// isLeafStruct
// ---------------------------------------------------------------------------

func TestIsLeafStruct_Duration_IsLeaf(t *testing.T) {
	if !isLeafStruct(reflect.TypeOf(config.Duration{})) {
		t.Error("config.Duration should be a leaf struct (implements yaml.Unmarshaler via pointer)")
	}
}

func TestIsLeafStruct_Config_IsNotLeaf(t *testing.T) {
	if isLeafStruct(reflect.TypeOf(config.Config{})) {
		t.Error("config.Config should not be a leaf struct")
	}
}

// directUnmarshaler implements yaml.Unmarshaler directly (not via pointer).
func TestIsLeafStruct_DirectUnmarshaler_IsLeaf(t *testing.T) {
	if !isLeafStruct(reflect.TypeOf(directUnmarshaler{})) {
		t.Error("directUnmarshaler should be a leaf struct (implements yaml.Unmarshaler directly)")
	}
}

// ---------------------------------------------------------------------------
// buildSchema
// ---------------------------------------------------------------------------

func TestBuildSchema_Bool(t *testing.T) {
	s := buildSchema(reflect.TypeOf(true), "")
	if s["type"] != "boolean" {
		t.Errorf("expected type boolean, got %v", s["type"])
	}
}

func TestBuildSchema_String(t *testing.T) {
	s := buildSchema(reflect.TypeOf(""), "")
	if s["type"] != "string" {
		t.Errorf("expected type string, got %v", s["type"])
	}
}

func TestBuildSchema_Int(t *testing.T) {
	s := buildSchema(reflect.TypeOf(0), "")
	if s["type"] != "integer" {
		t.Errorf("expected type integer, got %v", s["type"])
	}
}

func TestBuildSchema_Float(t *testing.T) {
	s := buildSchema(reflect.TypeOf(float64(0)), "")
	if s["type"] != "number" {
		t.Errorf("expected type number, got %v", s["type"])
	}
}

func TestBuildSchema_StringSlice(t *testing.T) {
	s := buildSchema(reflect.TypeOf([]string{}), "")
	if s["type"] != "array" {
		t.Errorf("expected type array, got %v", s["type"])
	}
	items, ok := s["items"].(map[string]interface{})
	if !ok {
		t.Fatal("expected items to be a map")
	}
	if items["type"] != "string" {
		t.Errorf("expected items type string, got %v", items["type"])
	}
}

func TestBuildSchema_Duration_IsLeaf(t *testing.T) {
	s := buildSchema(reflect.TypeOf(config.Duration{}), "")
	if s["type"] != "string" {
		t.Errorf("expected string type for Duration leaf, got %v", s["type"])
	}
}

func TestBuildSchema_SliceOfInts_IsArray(t *testing.T) {
	s := buildSchema(reflect.TypeOf([]int{}), "")
	if s["type"] != "array" {
		t.Errorf("expected type array for []int, got %v", s["type"])
	}
	// Non-string element: no "items" key.
	if _, ok := s["items"]; ok {
		t.Error("expected no items key for non-string slice")
	}
}

func TestBuildSchema_Map_IsEmpty(t *testing.T) {
	// map type hits the default case → empty schema map.
	s := buildSchema(reflect.TypeOf(map[string]string{}), "")
	if len(s) != 0 {
		t.Errorf("expected empty schema for map type, got %v", s)
	}
}

// TestBuildSchema_PointerType covers the pointer-dereference loop (main.go:103-105).
func TestBuildSchema_PointerType(t *testing.T) {
	s := buildSchema(reflect.TypeOf(&config.Config{}), "")
	if s["type"] != "object" {
		t.Errorf("expected type object for *config.Config, got %v", s["type"])
	}
}

// TestBuildObjectSchema_PointerType covers the pointer-dereference loop (main.go:157-159).
func TestBuildObjectSchema_PointerType(t *testing.T) {
	s := buildObjectSchema(reflect.TypeOf(&config.Config{}), "")
	if s["type"] != "object" {
		t.Errorf("expected type object for *config.Config, got %v", s["type"])
	}
}

func TestBuildSchema_WithConstraints(t *testing.T) {
	// "listeners.http.port" has {minimum:1, maximum:65535}
	s := buildSchema(reflect.TypeOf(0), "listeners.http.port")
	if s["minimum"] == nil {
		t.Error("expected minimum constraint for listeners.http.port")
	}
	if s["maximum"] == nil {
		t.Error("expected maximum constraint for listeners.http.port")
	}
}

// ---------------------------------------------------------------------------
// buildObjectSchema
// ---------------------------------------------------------------------------

func TestBuildObjectSchema_Config(t *testing.T) {
	s := buildObjectSchema(reflect.TypeOf(config.Config{}), "")
	if s["type"] != "object" {
		t.Errorf("expected type object, got %v", s["type"])
	}
	props, ok := s["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties to be a map")
	}
	// Config must have at least "listeners", "tls", "logging".
	for _, key := range []string{"listeners", "tls", "logging"} {
		if _, found := props[key]; !found {
			t.Errorf("expected property %q in Config schema", key)
		}
	}
}

func TestBuildObjectSchema_SkipsEmptyTag(t *testing.T) {
	// A struct with fields having no yaml tag or "-" tag should produce empty props.
	type noTagStruct struct {
		Hidden string
		Skip   string `yaml:"-"`
	}
	s := buildObjectSchema(reflect.TypeOf(noTagStruct{}), "")
	props, ok := s["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	if len(props) != 0 {
		t.Errorf("expected 0 properties for no-tag struct, got %d", len(props))
	}
}

func TestBuildObjectSchema_SkipsDashName(t *testing.T) {
	// A field whose yaml tag name is explicitly "-," (skip marker with comma) is skipped.
	type dashTagStruct struct {
		Skip  string `yaml:"-,omitempty"`
		Valid string `yaml:"valid"`
	}
	s := buildObjectSchema(reflect.TypeOf(dashTagStruct{}), "")
	props, ok := s["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	// Only "valid" should appear; "-" is skipped.
	if _, found := props["-"]; found {
		t.Error("expected field with tag name '-' to be skipped")
	}
	if _, found := props["valid"]; !found {
		t.Error("expected 'valid' field in properties")
	}
}

// ---------------------------------------------------------------------------
// walkProperties
// ---------------------------------------------------------------------------

func TestWalkProperties_NoProperties(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}
	result := walkProperties(schema, "")
	if len(result) != 0 {
		t.Errorf("expected 0 results for schema without properties, got %d", len(result))
	}
}

func TestWalkProperties_FlatLeaf(t *testing.T) {
	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"foo": map[string]interface{}{"type": "string"},
			"bar": map[string]interface{}{"type": "integer"},
		},
	}
	result := walkProperties(schema, "")
	if len(result) != 2 {
		t.Errorf("expected 2 leaf paths, got %d: %v", len(result), result)
	}
}

func TestWalkProperties_Nested(t *testing.T) {
	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"parent": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"child": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
	result := walkProperties(schema, "")
	if len(result) != 1 {
		t.Errorf("expected 1 leaf path, got %d: %v", len(result), result)
	}
	if result[0] != "parent.child" {
		t.Errorf("expected path 'parent.child', got %q", result[0])
	}
}

func TestWalkProperties_WithPrefix(t *testing.T) {
	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"port": map[string]interface{}{"type": "integer"},
		},
	}
	result := walkProperties(schema, "listeners.http")
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0] != "listeners.http.port" {
		t.Errorf("expected 'listeners.http.port', got %q", result[0])
	}
}

// ---------------------------------------------------------------------------
// extractAndPrintFields
// ---------------------------------------------------------------------------

func TestExtractAndPrintFields_Roundtrip(t *testing.T) {
	yamlInput := `
properties:
  foo:
    type: string
  bar:
    type: integer
`
	r := strings.NewReader(yamlInput)
	// extractAndPrintFields prints sorted fields to stdout; does not exit on success.
	// We capture by calling it directly (output goes to stdout, which is fine in tests).
	extractAndPrintFields(r)
}

// extractAndPrintFields with a non-map properties value returns 0 results
// (walkProperties returns nil).
func TestWalkProperties_NonMapProperties(t *testing.T) {
	schema := map[string]interface{}{
		"properties": "not-a-map",
	}
	result := walkProperties(schema, "")
	if len(result) != 0 {
		t.Errorf("expected 0 results for non-map properties, got %d", len(result))
	}
}

// walkProperties with a non-map child value appends the dotted key directly.
func TestWalkProperties_NonMapChild(t *testing.T) {
	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"num": 42, // non-map child value
		},
	}
	result := walkProperties(schema, "")
	if len(result) != 1 {
		t.Fatalf("expected 1 result for non-map child, got %d", len(result))
	}
	if result[0] != "num" {
		t.Errorf("expected path 'num', got %q", result[0])
	}
}

func TestExtractAndPrintFields_ViaParsedSchema(t *testing.T) {
	// Verify via walkProperties that the parser produces expected paths.
	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"foo": map[string]interface{}{"type": "string"},
			"bar": map[string]interface{}{"type": "integer"},
		},
	}
	fields := walkProperties(schema, "")
	if len(fields) != 2 {
		t.Errorf("expected 2 fields, got %d: %v", len(fields), fields)
	}
}

// ---------------------------------------------------------------------------
// run()
// ---------------------------------------------------------------------------

func TestRun_NoArgs_EmitsSchema(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(nil, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "$schema") {
		t.Errorf("expected $schema in output, got: %s", out.String())
	}
}

func TestRun_Fields(t *testing.T) {
	// Generate a schema first, then feed it to --fields.
	var schemaBuf, errOut bytes.Buffer
	if code := run(nil, nil, &schemaBuf, &errOut); code != 0 {
		t.Fatalf("schema generation failed (code=%d): %s", code, errOut.String())
	}

	var out, errOut2 bytes.Buffer
	schemaBytes := schemaBuf.Bytes()
	code := run([]string{"--fields"}, bytes.NewReader(schemaBytes), &out, &errOut2)
	if code != 0 {
		t.Fatalf("expected exit code 0 for --fields, got %d (stderr: %s)", code, errOut2.String())
	}
	// The output should contain dotted paths like "listeners.http.port".
	if !strings.Contains(out.String(), ".") {
		t.Errorf("expected dotted field paths in output, got: %s", out.String())
	}
}

func TestRun_Fields_BadInput(t *testing.T) {
	var out, errOut bytes.Buffer
	bad := errReader{err: errors.New("simulated read error")}
	code := run([]string{"--fields"}, bad, &out, &errOut)
	if code != 1 {
		t.Errorf("expected exit code 1 for bad input, got %d", code)
	}
}
