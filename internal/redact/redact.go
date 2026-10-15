// Package redact makes independent, credential-safe copies at output boundaries.
// A Redactor is immutable after construction and may be shared by goroutines.
package redact

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

const marker = "[REDACTED]"

// Redactor recognizes credential patterns and secret values learned from an
// explicitly supplied environment. It never reads the process environment.
// Its zero value recognizes patterns without learning additional values.
type Redactor struct {
	secrets     []string
	replacer    *strings.Replacer
	replacement string
	count       *int
}

// New learns nonempty GitHub authentication variables, including short values.
// Other TOKEN/SECRET/PASSWORD/PASSWD/API_KEY/PRIVATE_KEY/CREDENTIAL environment
// names are learned only when their values contain at least eight bytes.
func New(env map[string]string) *Redactor {
	set := make(map[string]bool)
	for name, value := range env {
		name = strings.ToUpper(name)
		explicit := name == "GH_TOKEN" || name == "GITHUB_TOKEN" || name == "GH_ENTERPRISE_TOKEN" || name == "GITHUB_ENTERPRISE_TOKEN"
		if value != "" && (explicit || len(value) >= 8 && secretEnvName(name)) {
			set[value] = true
		}
	}
	r := &Redactor{replacement: marker}
	for value := range set {
		r.secrets = append(r.secrets, value)
	}
	sort.Slice(r.secrets, func(i, j int) bool {
		if len(r.secrets[i]) != len(r.secrets[j]) {
			return len(r.secrets[i]) > len(r.secrets[j])
		}
		return r.secrets[i] < r.secrets[j]
	})
	// A credential can itself equal (or occur inside) the normal marker. In
	// that pathological case, removing it is the only non-leaking marker.
	r.replacement = r.removeSecrets(marker)
	pairs := make([]string, 0, 2*len(r.secrets))
	for _, secret := range r.secrets {
		pairs = append(pairs, secret, r.replacement)
	}
	if len(pairs) != 0 {
		r.replacer = strings.NewReplacer(pairs...)
	}
	return r
}

func secretEnvName(name string) bool {
	for _, part := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "PRIVATE_KEY", "CREDENTIAL"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

var (
	githubToken = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{20,}|v1\.[A-Fa-f0-9]{40})\b`)
	jwtToken    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	authValue   = regexp.MustCompile(`(?i)\b(bearer|token|basic)([ \t]+)([^\s,;"'<>]+)`)
	authHeader  = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization[ \t]*:[ \t]*)([^\s"'{}\[\]]+[^\r\n"'{}\[\]]*)`)
	urlPassword = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s/@:]+:)[^\s/@]*@`)
	privateKey  = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|\z)`)
)

func (r *Redactor) mark() string {
	if r == nil || r.replacement == "" && r.replacer == nil {
		return marker
	}
	return r.replacement
}

// WithCount runs f with an isolated counter, returning credential substitutions.
// The supplied redactor must not escape f or be used concurrently. The original
// redactor remains immutable and safe to share.
func (r *Redactor) WithCount(f func(*Redactor)) int {
	var scoped Redactor
	if r != nil {
		scoped = *r
	}
	count := 0
	scoped.count = &count
	f(&scoped)
	if r != nil && r.count != nil {
		*r.count += count
	}
	return count
}

func (r *Redactor) mask(value any) string {
	m := r.mark()
	if r != nil && r.count != nil && value != m {
		*r.count++
	}
	return m
}

func (r *Redactor) replacePattern(s string, pattern *regexp.Regexp, replacement string) string {
	if r == nil || r.count == nil {
		return pattern.ReplaceAllString(s, replacement)
	}
	return pattern.ReplaceAllStringFunc(s, func(match string) string {
		out := pattern.ReplaceAllString(match, replacement)
		if out != match {
			*r.count++
		}
		return out
	})
}

func (r *Redactor) replaceLearned(s string) string {
	if r.count == nil {
		return r.replacer.Replace(s)
	}
	var out strings.Builder
	start := 0
	for i := 0; i < len(s); {
		matched := false
		for _, secret := range r.secrets {
			if strings.HasPrefix(s[i:], secret) {
				out.WriteString(s[start:i])
				out.WriteString(r.mask(secret))
				i += len(secret)
				start, matched = i, true
				break
			}
		}
		if !matched {
			i++
		}
	}
	if start == 0 {
		return s
	}
	out.WriteString(s[start:])
	return out.String()
}

// String removes learned values wherever they occur, GitHub tokens, JWTs,
// authorization values, URL passwords, and PEM private-key blocks. Unfinished
// private-key blocks are redacted through the end of the string.
func (r *Redactor) String(s string) string {
	m := r.mark()
	s = r.replacePattern(s, privateKey, m)
	s = r.replacePattern(s, githubToken, m)
	s = r.replacePattern(s, jwtToken, m)
	s = r.replacePattern(s, authHeader, "${1}"+m)
	s = r.replacePattern(s, authValue, "${1}${2}"+m)
	s = r.replacePattern(s, urlPassword, "${1}"+m+"@")
	if r != nil && r.replacer != nil {
		s = r.replaceLearned(s)
		// Replacements can join previously separated text into a learned value
		// (including across marker boundaries). Removal strictly decreases the
		// string length, so even overlapping/adversarial values terminate.
		s = r.removeSecrets(s)
	}
	return s
}

func (r *Redactor) removeSecrets(s string) string {
	for {
		changed := false
		for _, secret := range r.secrets {
			if strings.Contains(s, secret) {
				if r.count != nil {
					*r.count += strings.Count(s, secret)
				}
				s = strings.ReplaceAll(s, secret, "")
				changed = true
			}
		}
		if !changed {
			return s
		}
	}
}

// sensitiveName matches exact names after ASCII case-folding and removal of
// underscores and hyphens. It deliberately does not match key, keyboard,
// tokenizer, publicKey, or arbitrary names containing "token".
func sensitiveName(name string) bool {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "-", "")
	switch name {
	case "token", "accesstoken", "refreshtoken", "idtoken", "authtoken",
		"ghtoken", "githubtoken", "ghenterprisetoken", "githubenterprisetoken",
		"password", "passwd", "secret", "clientsecret", "apikey", "privatekey",
		"credential", "credentials", "authorization", "proxyauthorization":
		return true
	}
	return false
}

func sensitivePath(name string) bool {
	for _, part := range strings.FieldsFunc(name, func(c rune) bool { return c == '[' || c == ']' || c == '.' }) {
		if sensitiveName(part) {
			return true
		}
	}
	return false
}

// Value copies JSON-like maps/slices recursively, redacting sensitive map
// values and every string (including map keys). Typed string maps/slices and
// JSON byte bodies are supported too. Other scalar values retain their type.
// Cyclic graphs and arbitrary application structs are not JSON-like inputs.
func (r *Redactor) Value(v any) any {
	switch v := v.(type) {
	case json.Number:
		return v
	case string:
		return r.String(v)
	case map[string]any:
		if v == nil {
			return map[string]any(nil)
		}
		out := make(map[string]any, len(v))
		for k, value := range v {
			key := r.String(k)
			if sensitivePath(k) {
				out[key] = r.mask(value)
			} else if s, ok := value.(string); ok && k == "query" && looksGraphQL(s) {
				out[key] = r.graphQL(s)
			} else {
				out[key] = r.Value(value)
			}
		}
		return out
	case map[string]string:
		if v == nil {
			return map[string]string(nil)
		}
		out := make(map[string]string, len(v))
		for k, value := range v {
			if sensitivePath(k) {
				out[r.String(k)] = r.mask(value)
			} else if k == "query" && looksGraphQL(value) {
				out[r.String(k)] = r.graphQL(value)
			} else {
				out[r.String(k)] = r.String(value)
			}
		}
		return out
	case []any:
		if v == nil {
			return []any(nil)
		}
		out := make([]any, len(v))
		for i := range v {
			out[i] = r.Value(v[i])
		}
		return out
	case []string:
		return r.strings(v)
	case json.RawMessage:
		if v == nil {
			return json.RawMessage(nil)
		}
		return json.RawMessage(r.body(v))
	case []byte:
		if v == nil {
			return []byte(nil)
		}
		return r.body(v)
	default:
		// JSON-compatible typed containers (for example []map[string]string
		// or map[string][]int) also need independent copies. Canonicalize
		// these to the ordinary JSON container types; typed scalar leaves
		// retain their values. Application structs/pointers are out of scope.
		value := reflect.ValueOf(v)
		if !value.IsValid() {
			return nil
		}
		switch value.Kind() {
		case reflect.String:
			return r.String(value.String())
		case reflect.Map:
			if value.Type().Key().Kind() != reflect.String {
				return v
			}
			if value.IsNil() {
				return map[string]any(nil)
			}
			out := make(map[string]any, value.Len())
			iter := value.MapRange()
			for iter.Next() {
				key, item := iter.Key().String(), iter.Value()
				if sensitivePath(key) {
					out[r.String(key)] = r.mask(item.Interface())
				} else if key == "query" && item.Kind() == reflect.String && looksGraphQL(item.String()) {
					out[r.String(key)] = r.graphQL(item.String())
				} else {
					out[r.String(key)] = r.Value(item.Interface())
				}
			}
			return out
		case reflect.Slice, reflect.Array:
			if value.Kind() == reflect.Slice && value.IsNil() {
				return []any(nil)
			}
			out := make([]any, value.Len())
			for i := range value.Len() {
				out[i] = r.Value(value.Index(i).Interface())
			}
			return out
		default:
			return v
		}
	}
}

func (r *Redactor) strings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = r.String(s)
	}
	return out
}

func (r *Redactor) body(in []byte) []byte {
	var value any
	if json.Valid(in) {
		decoder := json.NewDecoder(bytes.NewReader(in))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			if out, err := json.Marshal(r.Value(value)); err == nil {
				return out
			}
		}
	}
	if bytes.HasPrefix(bytes.TrimSpace(in), []byte("[")) {
		return []byte(r.mask(string(in)))
	}
	if looksGraphQL(string(in)) {
		return []byte(r.graphQL(string(in)))
	}
	return []byte(r.String(string(in)))
}
