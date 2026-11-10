package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSchemaYAML_IsParseable(t *testing.T) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(SchemaYAML, &doc); err != nil {
		t.Fatalf("SchemaYAML is not valid YAML: %v", err)
	}
}

func TestSchemaYAML_IsJSONSchema2019_09(t *testing.T) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(SchemaYAML, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	schema, ok := doc["$schema"].(string)
	if !ok || schema == "" {
		t.Fatal("SchemaYAML missing $schema key")
	}
	if !strings.Contains(schema, "2019-09") {
		t.Errorf("expected 2019-09 schema, got %q", schema)
	}
}

func TestSchemaYAML_HasExpectedTopLevelFields(t *testing.T) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(SchemaYAML, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	props, ok := doc["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("SchemaYAML missing top-level properties")
	}
	for _, field := range []string{"listeners", "tls", "sidecar", "security", "authn", "logging"} {
		if _, found := props[field]; !found {
			t.Errorf("top-level field %q missing from schema", field)
		}
	}
}

func TestSchemaYAML_HasConstraints(t *testing.T) {
	raw := string(SchemaYAML)
	for _, want := range []string{"minimum", "maximum", "enum", "format"} {
		if !strings.Contains(raw, want) {
			t.Errorf("SchemaYAML missing %q constraint keyword", want)
		}
	}
}

func TestSchemaYAML_AdditionalPropertiesFalse(t *testing.T) {
	raw := string(SchemaYAML)
	if !strings.Contains(raw, "additionalProperties: false") {
		t.Error("SchemaYAML missing additionalProperties: false")
	}
}
