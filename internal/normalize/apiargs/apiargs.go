// Package apiargs parses `gh api` arguments and reproduces gh's request
// parameter construction (pkg/cmd/api in cli/cli v2.102.0). It is shared by
// the REST (L2) and GraphQL (L3) normalizers. Everything here is pure.
package apiargs

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ErrHelp is returned when --help is present (gh prints help, no request).
var ErrHelp = errors.New("help requested")

// Parse parses the arguments that follow `gh api` (pflag semantics:
// interspersed flags, --flag=value, --flag value, -Xvalue, -X value, bundled
// short bools, `--` terminator). Exactly one positional endpoint is required.
func Parse(args []string) (model.APIRequest, error) {
	var req model.APIRequest
	var raw, magic []string
	var help, allowEscapeSequences bool
	var cache time.Duration
	fs := pflag.NewFlagSet("api", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Keep these types/defaults aligned with cli/cli pkg/cmd/api/api.go.
	fs.StringVar(&req.Hostname, "hostname", "", "")
	fs.StringVarP(&req.Method, "method", "X", "GET", "")
	fs.StringArrayVarP(&magic, "field", "F", nil, "")
	fs.StringArrayVarP(&raw, "raw-field", "f", nil, "")
	fs.StringArrayVarP(&req.Headers, "header", "H", nil, "")
	fs.StringSliceVarP(&req.Previews, "preview", "p", nil, "")
	fs.BoolVarP(&req.Include, "include", "i", false, "")
	fs.BoolVar(&req.Slurp, "slurp", false, "")
	fs.BoolVar(&req.Paginate, "paginate", false, "")
	fs.StringVar(&req.Input, "input", "", "")
	fs.BoolVar(&req.Silent, "silent", false, "")
	fs.StringVarP(&req.Template, "template", "t", "", "")
	fs.StringVarP(&req.JQ, "jq", "q", "", "")
	fs.DurationVar(&cache, "cache", 0, "")
	fs.BoolVar(&req.Verbose, "verbose", false, "")
	fs.BoolVar(&allowEscapeSequences, "allow-escape-sequences", false, "")
	fs.BoolVar(&help, "help", false, "") // inherited from gh's root command
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return req, ErrHelp
		}
		return req, err
	}
	req.MethodExplicit = fs.Changed("method")
	if fs.Changed("cache") {
		req.Cache = cache.String()
	}

	req.Fields = make([]model.APIField, 0, len(raw)+len(magic))
	for _, f := range raw {
		k, v, ok := strings.Cut(f, "=")
		req.Fields = append(req.Fields, model.APIField{Key: k, Value: v, Typed: false, NoValue: !ok})
	}
	for _, f := range magic {
		k, v, ok := strings.Cut(f, "=")
		req.Fields = append(req.Fields, model.APIField{Key: k, Value: v, Typed: true, NoValue: !ok})
	}
	if help {
		return req, ErrHelp
	}
	if err := cobra.ExactArgs(1)(nil, fs.Args()); err != nil {
		return req, err
	}
	req.Endpoint = fs.Arg(0)

	if req.MethodExplicit {
		req.Method = strings.ToUpper(req.Method)
	} else if len(raw)+len(magic) > 0 || req.Input != "" {
		req.Method = "POST"
	} else {
		req.Method = "GET"
	}
	return req, nil
}

// IsGraphQL reports whether the request goes to the GraphQL endpoint the way
// gh decides it: the endpoint is literally "graphql", or an absolute URL
// whose path is /graphql or /api/graphql (GHES).
func IsGraphQL(req model.APIRequest) bool {
	// gh only groups variables for the literal "graphql", but "/graphql" and
	// "graphql/" still reach https://api.github.com/graphql on github.com.
	if strings.Trim(req.Endpoint, "/") == "graphql" {
		return true
	}
	if i := strings.Index(req.Endpoint, "://"); i >= 0 {
		rest := req.Endpoint[i+3:]
		if j := strings.IndexAny(rest, "/"); j >= 0 {
			p := rest[j:]
			if k := strings.IndexAny(p, "?#"); k >= 0 {
				p = p[:k]
			}
			p = strings.TrimSuffix(p, "/")
			return p == "/graphql" || p == "/api/graphql"
		}
	}
	return false
}

// NeedsStdin reports whether gh would read stdin for this request:
// `--input -` or any `-F key=@-`.
func NeedsStdin(req model.APIRequest) bool {
	if req.Input == "-" {
		return true
	}
	for _, f := range req.Fields {
		if f.Typed && f.Value == "@-" {
			return true
		}
	}
	return false
}

// NeedsStdinArgv is a cheap, allocation-light check usable before full
// normalization: argv is os.Args[1:] (possibly alias-expanded). It returns
// false on parse errors.
func NeedsStdinArgv(argv []string) bool {
	if len(argv) == 0 || argv[0] != "api" {
		return false
	}
	req, err := Parse(argv[1:])
	if err != nil {
		return false
	}
	return NeedsStdin(req)
}

// BuildParams reproduces gh's parseFields: raw fields first, then typed
// fields, with nested keys (a[b], a[], a[][c]) and magic conversion for typed
// values (@file / @- contents, integers, true/false/null). Placeholders such
// as {owner} are left verbatim. File and stdin contents come only from inv;
// when they are unavailable the field is recorded as Unresolved and its value
// is set to nil.
func BuildParams(req model.APIRequest, inv model.Invocation) (map[string]any, []model.Unresolved, error) {
	params := make(map[string]any)
	var unresolved []model.Unresolved
	for _, f := range req.Fields {
		var value any = f.Value
		if f.NoValue {
			// gh: "key[]" without '=' creates an empty array; "key" is an error.
			if !strings.HasSuffix(f.Key, "[]") {
				return params, unresolved, fmt.Errorf("field %q requires a value separated by an '=' sign", f.Key)
			}
			value = nil
		} else if f.Typed {
			v, u := magicFieldValue(f.Key, f.Value, inv)
			value = v
			if u != nil {
				unresolved = append(unresolved, *u)
			}
		}
		if err := addField(params, f.Key, value, f.NoValue); err != nil {
			return params, unresolved, err
		}
	}
	return params, unresolved, nil
}

func magicFieldValue(key, v string, inv model.Invocation) (any, *model.Unresolved) {
	if strings.HasPrefix(v, "@") {
		name := v[1:]
		if name == "-" {
			if inv.Stdin.Status == model.StdinCaptured {
				return string(inv.Stdin.Data), nil
			}
			return nil, &model.Unresolved{Field: "fields." + key, Reason: "stdin not captured (" + string(inv.Stdin.Status) + ")", Source: "stdin"}
		}
		if inv.ReadFile == nil {
			return nil, &model.Unresolved{Field: "fields." + key, Reason: "file not readable: " + name, Source: "file"}
		}
		b, err := inv.ReadFile(name)
		if err != nil {
			return nil, &model.Unresolved{Field: "fields." + key, Reason: "file not readable: " + err.Error(), Source: "file"}
		}
		return string(b), nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n, nil
	}
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	return v, nil
}

// addField is a port of gh's parseField key handling.
func addField(params map[string]any, key string, value any, noValue bool) error {
	var keystack []string
	keyStartAt := 0
	for i, r := range key {
		switch r {
		case '[':
			if keyStartAt == 0 {
				keystack = append(keystack, key[0:i])
			}
			keyStartAt = i + 1
		case ']':
			keystack = append(keystack, key[keyStartAt:i])
		}
	}
	if keyStartAt == 0 {
		keystack = append(keystack, key)
	}
	if len(keystack) == 0 || (len(keystack) == 1 && keystack[0] == "" && key == "") {
		return fmt.Errorf("invalid key: %q", key)
	}

	destMap := params
	isArray := false
	var subkey string
	for _, k := range keystack {
		if k == "" {
			isArray = true
			continue
		}
		if subkey != "" {
			var err error
			if isArray {
				destMap, err = addParamsSlice(destMap, subkey, k)
				isArray = false
			} else {
				destMap, err = addParamsMap(destMap, subkey)
			}
			if err != nil {
				return err
			}
		}
		subkey = k
	}
	if isArray {
		if value == nil && noValue {
			destMap[subkey] = []any{}
			return nil
		}
		if v, exists := destMap[subkey]; exists {
			s, ok := v.([]any)
			if !ok {
				return fmt.Errorf("expected array type under %q, got %T", subkey, v)
			}
			destMap[subkey] = append(s, value)
		} else {
			destMap[subkey] = []any{value}
		}
		return nil
	}
	if _, exists := destMap[subkey]; exists {
		return fmt.Errorf("unexpected override existing field under %q", subkey)
	}
	destMap[subkey] = value
	return nil
}

func addParamsMap(m map[string]any, key string) (map[string]any, error) {
	if v, exists := m[key]; exists {
		if existMap, ok := v.(map[string]any); ok {
			return existMap, nil
		}
		return nil, fmt.Errorf("expected map type under %q, got %T", key, v)
	}
	newMap := make(map[string]any)
	m[key] = newMap
	return newMap, nil
}

func addParamsSlice(m map[string]any, prevkey, newkey string) (map[string]any, error) {
	if v, exists := m[prevkey]; exists {
		existSlice, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("expected array type under %q, got %T", prevkey, v)
		}
		if len(existSlice) > 0 {
			if lastMap, ok := existSlice[len(existSlice)-1].(map[string]any); ok {
				if _, keyExists := lastMap[newkey]; !keyExists {
					return lastMap, nil
				} else if lastMap[newkey] != nil && reflect.TypeOf(lastMap[newkey]).Kind() == reflect.Slice {
					return lastMap, nil
				}
			}
		}
		newMap := make(map[string]any)
		m[prevkey] = append(existSlice, newMap)
		return newMap, nil
	}
	newMap := make(map[string]any)
	m[prevkey] = []any{newMap}
	return newMap, nil
}
