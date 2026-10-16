package apiargs

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

// FieldArg identifies the exact argv transport of a parsed API field.
type FieldArg struct {
	Key   string
	Value string
	Typed bool
	Span  model.ArgSpan
}

type ArgLocators struct {
	Fields   []FieldArg
	Input    []model.ArgSpan
	Endpoint int
}

// LocateArgs records transports after Parse has accepted argv. Indices include
// the leading api command. It handles bundled boolean shorthand prefixes without
// consuming their neighbouring value-taking flag.
func LocateArgs(argv []string) ArgLocators {
	out := ArgLocators{Endpoint: -1}
	terminated := false
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--" && !terminated {
			terminated = true
			continue
		}
		if terminated || !strings.HasPrefix(a, "-") || a == "-" {
			out.Endpoint = i
			continue
		}
		start := i
		name, value, attached := "", "", false
		var keep []string
		if strings.HasPrefix(a, "--") {
			name, value, attached = strings.Cut(a[2:], "=")
			if !attached && (name == "include" || name == "silent" || name == "verbose" || name == "paginate" || name == "slurp" || name == "help" || name == "allow-escape-sequences") {
				continue
			}
		} else {
			for j := 1; j < len(a); j++ {
				if a[j] == 'i' {
					keep = append(keep, "-i")
					continue
				}
				switch a[j] {
				case 'f':
					name = "raw-field"
				case 'F':
					name = "field"
				case 'X':
					name = "method"
				case 'H':
					name = "header"
				case 'p':
					name = "preview"
				case 't':
					name = "template"
				case 'q':
					name = "jq"
				}
				value = strings.TrimPrefix(a[j+1:], "=")
				attached = j+1 < len(a)
				break
			}
			if name == "" {
				continue
			}
		}
		if !attached && i+1 < len(argv) {
			i++
			value = argv[i]
		}
		span := model.ArgSpan{Start: start, End: i + 1, Keep: keep}
		switch name {
		case "field", "raw-field":
			key, val, _ := strings.Cut(value, "=")
			out.Fields = append(out.Fields, FieldArg{Key: key, Value: val, Typed: name == "field", Span: span})
		case "input":
			out.Input = append(out.Input, span)
		}
	}
	return out
}

// FieldKey converts a non-array nested field path to gh's bracket spelling.
func FieldKey(path []string) string {
	if len(path) == 0 {
		return ""
	}
	key := path[0]
	for _, part := range path[1:] {
		key += "[" + part + "]"
	}
	return key
}
