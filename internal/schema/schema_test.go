package schema

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"go.yaml.in/yaml/v3"
)

// The committed schema files must match the tagged Go contracts.
func TestSchemaFilesUpToDate(t *testing.T) {
	for name, filename := range map[string]string{
		"config": "config.schema.json", "project-config": "project.schema.json", "local-config": "local.schema.json",
		"hook-input": "hook-input.schema.json", "hook-response": "hook-response.schema.json",
		"transform-response": "transform-response.schema.json",
	} {
		path := filepath.Join("..", "..", "schema", filename)
		got, err := os.ReadFile(path)
		want, generateErr := JSON(name)
		if err != nil || generateErr != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale (read=%v, generate=%v); run: go generate ./internal/schema", path, err, generateErr)
		}
	}
}

func mustSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	s, err := Generate(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
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
	if err := checkSchema(mustSchema(t, "config"), doc, "$"); err != nil {
		t.Fatal(err)
	}
	if err := checkSchema(mustSchema(t, "project-config"), map[string]any{"runtime": map[string]any{}}, "$"); err == nil {
		t.Fatal("project schema must reject runtime")
	}
	if err := checkSchema(mustSchema(t, "config"), map[string]any{"policy": map[string]any{"mode": "loose"}}, "$"); err == nil {
		t.Fatal("schema must reject unknown policy.mode")
	}
}

func TestProjectTrustIncludeSchema(t *testing.T) {
	valid := map[string]any{
		"version": 1,
		"trust":   map[string]any{"include": []any{".gitleaks.toml"}},
	}
	if err := checkSchema(mustSchema(t, "project-config"), valid, "$"); err != nil {
		t.Fatalf("project includes rejected: %v", err)
	}
	if err := checkSchema(mustSchema(t, "config"), valid, "$"); err == nil {
		t.Fatal("user config must reject project-only trust")
	}
	for _, invalid := range []map[string]any{{"include": []any{1}}, {"projects": []any{"/repo/.ghok.yaml"}}} {
		valid["trust"] = invalid
		if err := checkSchema(mustSchema(t, "project-config"), valid, "$"); err == nil {
			t.Fatalf("invalid trust section accepted: %v", invalid)
		}
	}
}

func TestConfigTemplatesMatchSchema(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "GHOK_CONFIG_DIR": filepath.Join(home, "cfg")}
	for scope, name := range map[string]string{config.ScopeUser: "config", config.ScopeProject: "project-config", config.ScopeLocal: "local-config"} {
		path, err := config.Init(scope, repo, env)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if err := checkSchema(mustSchema(t, name), doc, "$"); err != nil {
			t.Fatalf("%s template vs schema: %v", scope, err)
		}
	}
}

// checkSchema is a minimal validator for the JSON Schema subset Generate emits.
func checkSchema(s map[string]any, v any, at string) error {
	if negated, ok := s["not"].(map[string]any); ok && checkSchema(negated, v, at) == nil {
		return fmt.Errorf("%s: matches forbidden schema", at)
	}
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
	if c, ok := s["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		return fmt.Errorf("%s: want constant %v, got %v", at, c, v)
	}
	if pattern, ok := s["pattern"].(string); ok {
		if value, ok := v.(string); ok && !regexp.MustCompile(pattern).MatchString(value) {
			return fmt.Errorf("%s: string does not match pattern", at)
		}
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
			if names, ok := s["propertyNames"].(map[string]any); ok {
				if err := checkSchema(names, k, at); err != nil {
					return err
				}
			}
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
		switch n := v.(type) {
		case int:
		case float64:
			if math.Trunc(n) != n {
				return fmt.Errorf("%s: want integer", at)
			}
		default:
			return fmt.Errorf("%s: want integer", at)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: want boolean", at)
		}
	case "null":
		if v != nil {
			return fmt.Errorf("%s: want null", at)
		}
	}
	return nil
}

func asStrings(v any) []string {
	s, _ := v.([]string)
	return s
}
