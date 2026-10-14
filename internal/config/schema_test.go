package config

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

var updateSchema = flag.Bool("update-schema", false, "rewrite schema/*.schema.json")

// The committed schema files must match the Go config types.
func TestSchemaFilesUpToDate(t *testing.T) {
	for name, project := range map[string]bool{"config.schema.json": false, "project.schema.json": true} {
		path := filepath.Join("..", "..", "schema", name)
		want := SchemaJSON(project)
		if *updateSchema {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale (err=%v); run: go test ./internal/config -run TestSchemaFilesUpToDate -update-schema", path, err)
		}
	}
}

// The shipped example config must be valid under the schema (structure and enums).
func TestExampleConfigMatchesSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if err := checkSchema(Schema(false), doc, "$"); err != nil {
		t.Fatal(err)
	}
	if err := checkSchema(Schema(true), map[string]any{"runtime": map[string]any{}}, "$"); err == nil {
		t.Fatal("project schema must reject runtime")
	}
	if err := checkSchema(Schema(false), map[string]any{"policy": map[string]any{"mode": "loose"}}, "$"); err == nil {
		t.Fatal("schema must reject unknown policy.mode")
	}
}

// checkSchema is a minimal validator for the subset of JSON Schema that
// Schema emits (type, properties, additionalProperties, items, enum, oneOf, required).
func checkSchema(s map[string]any, v any, at string) error {
	if alts, ok := s["oneOf"].([]any); ok {
		for _, a := range alts {
			if checkSchema(a.(map[string]any), v, at) == nil {
				return nil
			}
		}
		return fmt.Errorf("%s: matches no alternative", at)
	}
	if e, ok := s["enum"].([]any); ok && !slices.ContainsFunc(e, func(x any) bool { return fmt.Sprint(x) == fmt.Sprint(v) }) {
		return fmt.Errorf("%s: %v not in %v", at, v, e)
	}
	switch s["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want object", at)
		}
		props, _ := s["properties"].(map[string]any)
		for _, r := range asStrings(s["required"]) {
			if _, ok := m[r]; !ok {
				return fmt.Errorf("%s: missing %s", at, r)
			}
		}
		for k, val := range m {
			p, ok := props[k].(map[string]any)
			if !ok {
				if ap, ok := s["additionalProperties"].(map[string]any); ok {
					p = ap
				} else {
					return fmt.Errorf("%s: unknown key %s", at, k)
				}
			}
			if err := checkSchema(p, val, at+"."+k); err != nil {
				return err
			}
		}
	case "array":
		l, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: want array", at)
		}
		for i, item := range l {
			if err := checkSchema(s["items"].(map[string]any), item, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s: want string", at)
		}
	case "integer":
		if _, ok := v.(int); !ok {
			return fmt.Errorf("%s: want integer", at)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: want boolean", at)
		}
	}
	return nil
}

func asStrings(v any) []string {
	s, _ := v.([]string)
	return s
}
