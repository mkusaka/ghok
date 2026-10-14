package config

//go:generate go run ./genschema -dir ../../schema

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// SchemaID is the published location of the generated global-config schema.
const SchemaID = "https://raw.githubusercontent.com/mkusaka/ghok/main/schema/config.schema.json"

// ProjectSchemaID is the published location of the .ghok.yaml schema.
const ProjectSchemaID = "https://raw.githubusercontent.com/mkusaka/ghok/main/schema/project.schema.json"

// projectForbidden are the top-level sections a project .ghok.yaml may not set.
var projectForbidden = []string{"runtime", "logging", "trust"}

// The schema is derived from the Config types alone (like zod's toJSONSchema):
// field names come from `yaml` tags, and constraints from these tags:
//
//	enum:"a,b"       allowed values (integers when they parse as such)
//	doc:"..."        description
//	required:"true"  property must be present
//	deprecated:"true" property is retained only for migration

// Schema returns the JSON Schema (draft 2020-12) for the global config.yaml,
// or, with project set, for a project .ghok.yaml (which may not set
// runtime, logging or trust).
func Schema(project bool) map[string]any {
	s := schemaFor(reflect.TypeFor[Config](), "")
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"], s["title"] = SchemaID, "ghok config.yaml"
	if project {
		s["$id"], s["title"] = ProjectSchemaID, "ghok project .ghok.yaml"
		props := s["properties"].(map[string]any)
		for _, k := range projectForbidden {
			delete(props, k)
		}
	}
	return s
}

// SchemaJSON is Schema rendered as indented JSON with a trailing newline.
func SchemaJSON(project bool) []byte {
	b, err := json.MarshalIndent(Schema(project), "", "  ")
	if err != nil {
		panic(err) // only maps, slices and scalars: cannot fail
	}
	return append(b, '\n')
}

func schemaFor(t reflect.Type, tag reflect.StructTag) map[string]any {
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
	case reflect.TypeFor[StringList]():
		item := map[string]any{"type": "string"}
		if enum != nil {
			item["enum"] = enum
		}
		return map[string]any{"oneOf": []any{item, map[string]any{"type": "array", "items": item}}}
	case reflect.TypeFor[Duration]():
		return map[string]any{"type": "string", "pattern": `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`}
	}
	var s map[string]any
	switch t.Kind() {
	case reflect.Pointer:
		return schemaFor(t.Elem(), tag)
	case reflect.Bool:
		s = map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		s = map[string]any{"type": "integer"}
	case reflect.String:
		s = map[string]any{"type": "string"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), "")}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), "")}
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			p := schemaFor(f.Type, f.Tag)
			if d := f.Tag.Get("doc"); d != "" {
				p["description"] = d
			}
			if f.Tag.Get("deprecated") == "true" {
				p["deprecated"] = true
			}
			if f.Tag.Get("required") == "true" {
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
		panic("config schema: unsupported type " + t.String())
	}
	if enum != nil {
		s["enum"] = enum
	}
	return s
}
