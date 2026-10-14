// Package apiargs parses `gh api` arguments and reproduces gh's request
// parameter construction (pkg/cmd/api in cli/cli v2.102.0). It is shared by
// the REST (L2) and GraphQL (L3) normalizers. Everything here is pure.
package apiargs

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

type flagKind int

const (
	kBool flagKind = iota
	kString
)

type flagSpec struct {
	long  string
	short byte
	kind  flagKind
}

var flags = []flagSpec{
	{"allow-escape-sequences", 0, kBool},
	{"cache", 0, kString},
	{"field", 'F', kString},
	{"header", 'H', kString},
	{"help", 0, kBool},
	{"hostname", 0, kString},
	{"include", 'i', kBool},
	{"input", 0, kString},
	{"jq", 'q', kString},
	{"method", 'X', kString},
	{"paginate", 0, kBool},
	{"preview", 'p', kString},
	{"raw-field", 'f', kString},
	{"silent", 0, kBool},
	{"slurp", 0, kBool},
	{"template", 't', kString},
	{"verbose", 0, kBool},
}

func lookupLong(name string) *flagSpec {
	for i := range flags {
		if flags[i].long == name {
			return &flags[i]
		}
	}
	return nil
}

func lookupShort(c byte) *flagSpec {
	for i := range flags {
		if flags[i].short == c {
			return &flags[i]
		}
	}
	return nil
}

// ErrHelp is returned when --help is present (gh prints help, no request).
var ErrHelp = errors.New("help requested")

// Parse parses the arguments that follow `gh api` (pflag semantics:
// interspersed flags, --flag=value, --flag value, -Xvalue, -X value, bundled
// short bools, `--` terminator). Exactly one positional endpoint is required.
func Parse(args []string) (model.APIRequest, error) {
	var req model.APIRequest
	var positionals []string
	var raw, magic []string
	help := false

	set := func(f *flagSpec, val string, hasVal bool) error {
		if f.kind == kBool {
			b := true
			if hasVal {
				var err error
				if b, err = strconv.ParseBool(val); err != nil {
					return fmt.Errorf("invalid argument %q for --%s", val, f.long)
				}
			}
			switch f.long {
			case "help":
				help = b
			case "include":
				req.Include = b
			case "paginate":
				req.Paginate = b
			case "silent":
				req.Silent = b
			case "slurp":
				req.Slurp = b
			case "verbose":
				req.Verbose = b
			}
			return nil
		}
		switch f.long {
		case "cache":
			req.Cache = val
		case "field":
			magic = append(magic, val)
		case "raw-field":
			raw = append(raw, val)
		case "header":
			req.Headers = append(req.Headers, val)
		case "hostname":
			req.Hostname = val
		case "input":
			req.Input = val
		case "jq":
			req.JQ = val
		case "method":
			req.Method = val
			req.MethodExplicit = true
		case "preview":
			// StringSlice: comma separated, repeatable.
			for _, p := range strings.Split(val, ",") {
				req.Previews = append(req.Previews, p)
			}
		case "template":
			req.Template = val
		}
		return nil
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			positionals = append(positionals, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			f := lookupLong(name)
			if f == nil {
				return req, fmt.Errorf("unknown flag: --%s", name)
			}
			if f.kind == kString && !hasVal {
				if i+1 >= len(args) {
					return req, fmt.Errorf("flag needs an argument: --%s", name)
				}
				i++
				val, hasVal = args[i], true
			}
			if err := set(f, val, hasVal); err != nil {
				return req, err
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j := 1; j < len(a); j++ {
				f := lookupShort(a[j])
				if f == nil {
					return req, fmt.Errorf("unknown shorthand flag: %q in %s", a[j], a)
				}
				if f.kind == kBool {
					// pflag accepts -i=true style only for the last shorthand.
					if j+1 < len(a) && a[j+1] == '=' {
						if err := set(f, a[j+2:], true); err != nil {
							return req, err
						}
						break
					}
					if err := set(f, "", false); err != nil {
						return req, err
					}
					continue
				}
				var val string
				switch {
				case j+1 < len(a) && a[j+1] == '=':
					val = a[j+2:]
				case j+1 < len(a):
					val = a[j+1:]
				default:
					if i+1 >= len(args) {
						return req, fmt.Errorf("flag needs an argument: %q in %s", a[j], a)
					}
					i++
					val = args[i]
				}
				if err := set(f, val, true); err != nil {
					return req, err
				}
				break
			}
		default:
			positionals = append(positionals, a)
		}
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
	if len(positionals) != 1 {
		return req, fmt.Errorf("accepts 1 arg(s), received %d", len(positionals))
	}
	req.Endpoint = positionals[0]

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
	if req.Endpoint == "graphql" {
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
