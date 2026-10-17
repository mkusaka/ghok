// Package schema generates JSON Schemas from ghok's tagged Go contracts.
package schema

//go:generate go run ./genschema -dir ../../schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

const baseID = "https://raw.githubusercontent.com/mkusaka/ghok/main/schema/"

// Generate returns a draft 2020-12 schema for a config or hook document.
// YAML requires only required:"true" fields; JSON requires fields without
// omitempty. Both use enum, const and doc tags for values and descriptions.
func Generate(name string) (map[string]any, error) {
	var s map[string]any
	filename := name + ".schema.json"
	switch name {
	case "config", "project-config", "local-config":
		s = schemaFor(reflect.TypeFor[config.Config](), "", "yaml")
		props := s["properties"].(map[string]any)
		if name != "config" {
			delete(props, "runtime")
			delete(props, "logging")
		}
		if name == "project-config" {
			filename = "project.schema.json"
			s["title"] = "ghok project .ghok.yaml"
		} else {
			s["title"] = "ghok config.yaml"
			if name == "local-config" {
				filename = "local.schema.json"
				s["title"] = "ghok personal .ghok.local.yaml"
			}
			// Only .ghok.yaml is trust-gated: no trust section or hook files.
			delete(props, "trust")
			// The hook schema map is shared by hook and parallel items.
			pre := props["hooks"].(map[string]any)["properties"].(map[string]any)["pre"].(map[string]any)
			hook := pre["items"].(map[string]any)["oneOf"].([]any)[0].(map[string]any)
			delete(hook["properties"].(map[string]any), "files")
			transforms := props["transforms"].(map[string]any)["properties"].(map[string]any)["pre"].(map[string]any)
			delete(transforms["items"].(map[string]any)["properties"].(map[string]any), "files")
		}
	case "hook-input":
		s = schemaFor(reflect.TypeFor[protocol.Input](), "", "json")
		s["title"] = "ghok hook input"
	case "hook-response":
		s = schemaFor(reflect.TypeFor[protocol.Response](), "", "json")
		s["title"] = "ghok hook response"
	case "transform-response":
		s = schemaFor(reflect.TypeFor[protocol.TransformResponse](), "", "json")
		s["title"] = "ghok transform response"
		props := s["properties"].(map[string]any)
		props["set"].(map[string]any)["propertyNames"] = map[string]any{"pattern": protocol.TransformPathPattern}
		props["unset"].(map[string]any)["items"] = map[string]any{"type": "string", "pattern": protocol.TransformPathPattern}
		props["unset"].(map[string]any)["uniqueItems"] = true
	default:
		return nil, fmt.Errorf("unknown schema %q", name)
	}
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"] = baseID + filename
	return s, nil
}

// JSON renders a schema as indented JSON with a trailing newline.
func JSON(name string) ([]byte, error) {
	s, err := Generate(name)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func schemaFor(t reflect.Type, tag reflect.StructTag, names string) map[string]any {
	var enum []any
	if e := tag.Get("enum"); e != "" {
		for _, v := range strings.Split(e, ",") {
			if n, err := strconv.Atoi(v); err == nil {
				enum = append(enum, n)
			} else {
				enum = append(enum, v)
			}
		}
	}
	switch t {
	case reflect.TypeFor[config.StringList]():
		item := map[string]any{"type": "string"}
		if enum != nil {
			item["enum"] = enum
		}
		return map[string]any{"oneOf": []any{item, map[string]any{"type": "array", "items": item}}}
	case reflect.TypeFor[config.TransformList]():
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"pre": map[string]any{"type": "array", "items": schemaFor(t.Elem(), "", names)},
		}}
	case reflect.TypeFor[config.HookList]():
		hook := schemaFor(t.Elem(), "", names)
		ids := schemaFor(reflect.TypeFor[config.StringList](), "", names)
		item := func(key, doc string, value any) map[string]any {
			return map[string]any{"type": "object", "description": doc, "properties": map[string]any{key: value}, "required": []string{key}, "additionalProperties": false}
		}
		list := func(phase string) map[string]any {
			return map[string]any{"type": "array", "description": "Hooks and control items of the " + phase + " phase, run in order.", "items": map[string]any{"oneOf": []any{
				hook,
				item("parallel", "Run these hooks concurrently as background hooks, then wait for all of them (GitHub Actions steps parallel).", map[string]any{"type": "array", "minItems": 1, "items": hook}),
				item("wait", "Wait for earlier background hooks of this list by ID.", ids),
				item("wait-all", "Wait for every earlier background hook of this list; takes no value.", map[string]any{"type": "null"}),
			}}}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"pre": list("pre"), "post": list("post")}}
	case reflect.TypeFor[config.Duration]():
		return map[string]any{"type": "string", "pattern": `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`}
	case reflect.TypeFor[map[string]any]():
		return map[string]any{"type": "object", "additionalProperties": map[string]any{}}
	}
	var s map[string]any
	switch t.Kind() {
	case reflect.Pointer:
		return schemaFor(t.Elem(), tag, names)
	case reflect.Bool:
		s = map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		s = map[string]any{"type": "integer"}
	case reflect.String:
		s = map[string]any{"type": "string"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), "", names)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), "", names)}
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := range t.NumField() {
			f := t.Field(i)
			name, options, _ := strings.Cut(f.Tag.Get(names), ",")
			if name == "" || name == "-" {
				continue
			}
			p := schemaFor(f.Type, f.Tag, names)
			if d := f.Tag.Get("doc"); d != "" {
				p["description"] = d
			}
			if f.Tag.Get("required") == "true" || (names == "json" && !strings.Contains(","+options+",", ",omitempty,")) {
				required = append(required, name)
			}
			props[name] = p
		}
		s = map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if required != nil {
			s["required"] = required
		}
		return s
	default:
		panic("schema: unsupported type " + t.String())
	}
	if enum != nil {
		s["enum"] = enum
	}
	if value := tag.Get("const"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			panic("schema: invalid integer const " + value)
		}
		s["const"] = n
	}
	return s
}
