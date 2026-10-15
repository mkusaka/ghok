package redact

import (
	"slices"
	"strings"
)

// Argv returns a fresh slice. Flag values are redacted even when they are too
// short or have no recognizable token shape. -- terminates flag interpretation.
func (r *Redactor) Argv(argv []string) []string {
	out := slices.Clone(argv)
	command := commandWords(argv)
	api := command[0] == "api"
	secretSet := command[0] == "secret" && command[1] == "set"
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			for j := i; j < len(argv); j++ {
				out[j] = r.String(argv[j])
			}
			break
		}
		prefix, value, attached, kind := "", "", false, ""
		if strings.HasPrefix(a, "--") {
			name, val, hasValue := strings.Cut(a[2:], "=")
			switch {
			case sensitiveName(name):
				kind = "secret"
			case secretSet && name == "body":
				kind = "secret"
			case api && name == "header":
				kind = "header"
			case api && (name == "field" || name == "raw-field"):
				kind = "field"
			}
			prefix, value, attached = "--"+name+"=", val, hasValue
		} else if strings.HasPrefix(a, "-") && len(a) > 1 {
			// gh api permits boolean shorthand bundles preceding a value flag.
			for j := 1; j < len(a); j++ {
				switch {
				case secretSet && a[j] == 'b':
					kind = "secret"
				case api && a[j] == 'H':
					kind = "header"
				case api && (a[j] == 'f' || a[j] == 'F'):
					kind = "field"
				}
				if kind != "" {
					prefix = a[:j+1]
					value = a[j+1:]
					attached = value != ""
					if strings.HasPrefix(value, "=") {
						prefix += "="
						value = value[1:]
					}
					break
				}
				if !api || !strings.ContainsRune("isv", rune(a[j])) {
					break
				}
			}
		}
		if kind == "" {
			out[i] = r.String(a)
			continue
		}
		if !attached {
			out[i] = r.String(a)
			if i+1 >= len(argv) || argv[i+1] == "--" {
				continue
			}
			i++
			value = argv[i]
		}
		var redacted string
		switch kind {
		case "secret":
			redacted = r.mask(value)
		case "header":
			redacted = r.header(value)
		case "field":
			redacted = r.field(value)
		}
		if attached {
			out[i] = r.String(prefix + redacted)
		} else {
			out[i] = r.String(redacted)
		}
	}
	return out
}

func (r *Redactor) header(value string) string {
	name, secret, ok := strings.Cut(value, ":")
	if ok && sensitiveName(strings.TrimSpace(name)) {
		return r.String(name + ": " + r.mask(strings.TrimSpace(secret)))
	}
	return r.String(value)
}

func (r *Redactor) field(value string) string {
	key, val, ok := strings.Cut(value, "=")
	if !ok {
		return r.String(value)
	}
	if sensitivePath(key) {
		return r.String(key + "=" + r.mask(val))
	}
	if key == "query" && looksGraphQL(val) {
		return r.String(key) + "=" + r.graphQL(val)
	}
	return r.String(value)
}

// Only gh's value-taking global options need skipping to identify command
// words. Command-specific flags are handled after the command has been found.
func commandWords(argv []string) [2]string {
	var words [2]string
	count := 0
	for i := 0; i < len(argv) && count < len(words); i++ {
		a := argv[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			name, _, attached := strings.Cut(a, "=")
			if !attached && (name == "-R" || name == "--repo" || name == "--hostname") {
				i++
			}
			continue
		}
		words[count] = a
		count++
	}
	return words
}
