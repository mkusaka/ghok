package logging

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/redact"
)

func contentSummary(s string) string {
	return fmt.Sprintf("[content: %d bytes]", len(s))
}

func minimizeTexts(original, safe *model.Texts) {
	if original == nil {
		return
	}
	for _, pair := range [3][2]*model.Text{{original.Title, safe.Title}, {original.Body, safe.Body}, {original.Notes, safe.Notes}} {
		if pair[0] != nil {
			pair[1].Value = contentSummary(pair[0].Value)
		}
	}
}

func graphQLSummary(s string) string {
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("[graphql document: %d bytes, sha256:%x]", len(s), hash[:8])
}

func contentKey(key string) string {
	parts := strings.FieldsFunc(key, func(c rune) bool { return c == '[' || c == ']' || c == '.' })
	if len(parts) != 0 {
		key = parts[len(parts)-1]
	}
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", ""), "_", ""))
}

func proseKey(key string) bool {
	for _, part := range strings.FieldsFunc(key, func(c rune) bool { return c == '[' || c == ']' || c == '.' }) {
		switch contentKey(part) {
		case "body", "title", "message", "description", "subject", "commitheadline", "commitbody", "comment", "notes":
			return true
		}
	}
	return false
}

func structuralKey(key string) bool {
	switch contentKey(key) {
	case "repository", "repo", "destinationrepository", "owner", "org", "organization",
		"branch", "base", "head", "basebranch", "headbranch", "baserefname", "headrefname", "ref", "sha",
		"url", "host", "hostname", "action", "family", "flag", "command":
		return true
	}
	return false
}

func numericString(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' || s[0] == '+' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func minimizeString(s, key string) string {
	if contentKey(key) == "query" {
		return graphQLSummary(s)
	}
	if proseKey(key) {
		return contentSummary(s)
	}
	if structuralKey(key) || numericString(s) {
		return s
	}
	if len(s) > 64 || strings.ContainsFunc(s, unicode.IsSpace) {
		return contentSummary(s)
	}
	return s
}

// Work on the credential redactor's copy, but measure and classify the original
// strings: credential replacement and GraphQL formatting can change their size.
func minimizeValue(r *redact.Redactor, original, safe any, key string) any {
	if _, ok := original.(json.Number); ok {
		return safe
	}
	if _, withheld := safe.(string); withheld {
		value := reflect.ValueOf(original)
		if value.IsValid() && value.Kind() != reflect.String {
			return safe // A sensitive map key already withheld this container.
		}
	}
	switch original := original.(type) {
	case json.RawMessage:
		return json.RawMessage(minimizeBytes(r, original, key))
	case []byte:
		return minimizeBytes(r, original, key)
	}
	value := reflect.ValueOf(original)
	if !value.IsValid() {
		return safe
	}
	switch value.Kind() {
	case reflect.String:
		s := value.String()
		if minimized := minimizeString(s, key); minimized != s {
			return r.String(minimized)
		}
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return safe
		}
		iter := value.MapRange()
		for iter.Next() {
			name, item := iter.Key().String(), iter.Value().Interface()
			safeName := r.String(name)
			switch out := safe.(type) {
			case map[string]any:
				out[safeName] = minimizeValue(r, item, out[safeName], name)
			case map[string]string:
				out[safeName] = minimizeValue(r, item, out[safeName], name).(string)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range value.Len() {
			switch out := safe.(type) {
			case []any:
				out[i] = minimizeValue(r, value.Index(i).Interface(), out[i], key)
			case []string:
				out[i] = minimizeValue(r, value.Index(i).Interface(), out[i], key).(string)
			}
		}
	}
	return safe
}

func minimizeBytes(r *redact.Redactor, original []byte, key string) []byte {
	if original == nil {
		return nil
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	if json.Valid(original) && decoder.Decode(&decoded) == nil {
		minimized := minimizeValue(r, decoded, r.Value(decoded), key)
		if out, err := json.Marshal(minimized); err == nil {
			return out
		}
	}
	// Opaque byte bodies have no trustworthy structural context.
	return []byte(r.String(contentSummary(string(original))))
}

func argvCommand(argv []string) [2]string {
	var command [2]string
	count := 0
	for i := 0; i < len(argv) && count < len(command); i++ {
		arg := argv[i]
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") {
			name, _, attached := strings.Cut(arg, "=")
			if !attached && (name == "-R" || name == "--repo" || name == "--hostname") {
				i++
			}
			continue
		}
		command[count] = arg
		count++
	}
	return command
}

func minimizeField(field string) string {
	key, value, ok := strings.Cut(field, "=")
	if !ok {
		return minimizeString(field, "")
	}
	return key + "=" + minimizeString(value, key)
}

func minimizeArgv(r *redact.Redactor, argv []string) []string {
	out := r.Argv(argv)
	command := argvCommand(argv)
	api := command[0] == "api"
	fields := api || command == [2]string{"workflow", "run"}
	review := command == [2]string{"pr", "review"}
	words, positionals := 0, 0
	flags := true
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if flags && arg == "--" {
			flags = false
			continue
		}
		if flags && strings.HasPrefix(arg, "-") && arg != "-" {
			prefix, value, key, attached := "", "", "", false
			field := false
			if strings.HasPrefix(arg, "--") {
				name, val, hasValue := strings.Cut(arg[2:], "=")
				key, value, attached = name, val, hasValue
				prefix = "--" + name + "="
				field = fields && (name == "field" || name == "raw-field")
				if review && name == "comment" && !hasValue {
					continue // --comment selects the review event; it is not prose.
				}
				if !hasValue && !field && !proseKey(key) && !structuralKey(key) && name != "method" {
					continue
				}
			} else {
				for j := 1; j < len(arg); j++ {
					switch arg[j] {
					case 'f', 'F':
						if fields {
							key, field = "field", true
						}
					case 'b':
						if command == [2]string{"pr", "checkout"} || command == [2]string{"repo", "view"} ||
							command[0] == "run" || command[0] == "browse" || command[0] == "codespace" {
							key = "branch"
						} else if !api {
							key = "body"
						}
					case 't':
						if !api {
							key = "title"
						}
					case 'm':
						if !api && command != [2]string{"pr", "merge"} {
							key = "message"
							if command[0] == "issue" || (command[0] == "pr" && (command[1] == "create" || command[1] == "edit" || command[1] == "list")) {
								key = "milestone"
							}
						}
					case 'R':
						key = "repo"
					case 'B':
						if !api {
							key = "base"
						}
					case 'H':
						if !api {
							key = "head"
						}
					case 'X':
						if api {
							key = "method"
						}
					case 'o':
						key = "owner"
					}
					if key != "" {
						prefix, value = arg[:j+1], arg[j+1:]
						attached = value != ""
						if strings.HasPrefix(value, "=") {
							prefix, value = prefix+"=", value[1:]
						}
						break
					}
					if !api || !strings.ContainsRune("isv", rune(arg[j])) {
						break
					}
				}
				if key == "" {
					if len(arg) > 2 {
						minimized := minimizeString(arg[2:], "")
						if minimized != arg[2:] {
							out[i] = r.String(arg[:2] + minimized)
						}
					}
					continue
				}
			}
			if !attached {
				if i+1 >= len(argv) || argv[i+1] == "--" {
					continue
				}
				i++
				value = argv[i]
			}
			minimized := minimizeString(value, key)
			if field {
				minimized = minimizeField(value)
			}
			if minimized != value {
				if attached {
					minimized = prefix + minimized
				}
				out[i] = r.String(minimized)
			}
			continue
		}
		key := ""
		if words < 2 && flags {
			if words == 0 && !strings.ContainsFunc(arg, unicode.IsSpace) {
				key = "command"
			}
			if api && words == 1 {
				key = "url" // REST endpoint or graphql.
			}
			words++
		} else {
			positionals++
			if positionals == 1 && !strings.ContainsFunc(arg, unicode.IsSpace) &&
				(command[0] == "repo" || (command[0] == "pr" && command[1] != "create" && command[1] != "list" && command[1] != "status")) {
				key = "ref"
			}
			if review && positionals > 1 {
				key = "comment"
			}
		}
		if strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://") {
			if key != "comment" {
				key = "url"
			}
		}
		if minimized := minimizeString(arg, key); minimized != arg {
			out[i] = r.String(minimized)
		}
	}
	return out
}
