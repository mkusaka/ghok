package config

import (
	"encoding/json"
	"reflect"
	"strings"
)

// SchemaID is the published location of the generated global-config schema.
const SchemaID = "https://raw.githubusercontent.com/mkusaka/ghok/main/schema/config.schema.json"

// ProjectSchemaID is the published location of the .ghok.yaml schema.
const ProjectSchemaID = "https://raw.githubusercontent.com/mkusaka/ghok/main/schema/project.schema.json"

// projectForbidden are the top-level sections a project .ghok.yaml may not set.
var projectForbidden = []string{"runtime", "logging", "trust"}

// Values that validation accepts, keyed by "Type.Field" (for lists: the item).
var schemaEnums = map[string][]any{
	"Config.Version":         {1},
	"Policy.Mode":            {ModeObserve, ModeEnforce},
	"Policy.UnknownMutation": {UnknownAllow, UnknownDeny, UnknownAudit},
	"Policy.ProtectedUnknown": {
		UnknownAllow, UnknownDeny, UnknownAudit,
	},
	"Rule.Decision": {UnknownDeny, UnknownAudit},
	"Hook.Phase":    {PhasePre, PhasePost},
	"Hook.OnError":  {OnErrorDeny, OnErrorAllow, OnErrorIgnore},
	"Logging.Level": {"info", "decision"},
	"Match.Effects": {"read", "write", "unknown"},
	"Match.Levels":  {"L1", "L2", "L3", "none"},
}

var schemaRequired = map[string][]string{
	"Hook": {"id", "phase", "command"},
	"Rule": {"id", "decision"},
}

var schemaDescriptions = map[string]string{
	"Config.Version":               "Config format version; must be 1.",
	"Policy.Mode":                  "observe: classify and log only; enforce: deny according to rules, unknown handling and pre-hooks.",
	"Policy.UnknownMutation":       "Handling of write/unknown operations that could not be classified.",
	"Policy.ProtectedRepositories": "owner/repo globs; unclassified or unresolved writes against them use protectedUnknown.",
	"Policy.ProtectedUnknown":      "Handling of unclassified/unresolved writes that may target a protected repository.",
	"Match.Operations":             "Action globs such as pr.merge.* (see docs/normalization.md).",
	"Match.Commands":               "gh command path prefixes such as \"pr merge\" or \"api\".",
	"Match.Repositories":           "owner/repo globs (case-insensitive).",
	"Match.Levels":                 "Normalization level: L1 native gh command, L2 gh api REST, L3 gh api graphql.",
	"Hook.Command":                 "Executable and arguments; relative paths resolve against the config file directory.",
	"Hook.Timeout":                 "Go duration, e.g. 10s.",
	"Hook.Priority":                "Lower runs first; ties keep config order.",
	"Hook.DependsOn":               "IDs of hooks (same phase) that must finish first.",
	"Hook.Optional":                "Skip instead of failing when the command is missing or not executable.",
	"Hook.CaptureOutput":           "Post hooks only: include gh stdout/stderr (base64, bounded) in the hook input.",
	"Trust.Projects":               "Absolute paths of project .ghok.yaml files that may add hooks/rules.",
	"Runtime.RealGh":               "Absolute path of the real gh; discovered on PATH when empty.",
}

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

func schemaFor(t reflect.Type, key string) map[string]any {
	switch t {
	case reflect.TypeFor[StringList]():
		item := map[string]any{"type": "string"}
		if e := schemaEnums[key]; e != nil {
			item["enum"] = e
		}
		return map[string]any{"oneOf": []any{item, map[string]any{"type": "array", "items": item}}}
	case reflect.TypeFor[Duration]():
		return map[string]any{"type": "string", "pattern": `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`}
	}
	var s map[string]any
	switch t.Kind() {
	case reflect.Pointer:
		return schemaFor(t.Elem(), key)
	case reflect.Bool:
		s = map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		s = map[string]any{"type": "integer"}
	case reflect.String:
		s = map[string]any{"type": "string"}
	case reflect.Slice:
		s = map[string]any{"type": "array", "items": schemaFor(t.Elem(), key)}
	case reflect.Map:
		s = map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), "")}
	case reflect.Struct:
		props := map[string]any{}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			fieldKey := t.Name() + "." + f.Name
			p := schemaFor(f.Type, fieldKey)
			if d := schemaDescriptions[fieldKey]; d != "" {
				p["description"] = d
			}
			props[name] = p
		}
		s = map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if r := schemaRequired[t.Name()]; r != nil {
			s["required"] = r
		}
		return s
	default:
		panic("config schema: unsupported type " + t.String())
	}
	if e := schemaEnums[key]; e != nil && t.Kind() != reflect.Slice {
		s["enum"] = e
	}
	return s
}
