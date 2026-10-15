package gitmeta

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ConfigEntry struct{ Section, Subsection, Key, Value string }

// Read includes in place: later values override earlier values, just as git does.
func ReadConfig(path, home string, depth int) ([]ConfigEntry, error) {
	if depth > 10 {
		return nil, fmt.Errorf("git config include depth exceeded")
	}
	data, err := ReadBounded(path)
	if err != nil {
		return nil, err
	}
	var entries []ConfigEntry
	var section, subsection, logical string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), maxBytes)
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Text()
		logical += raw
		// An odd number of trailing backslashes continues the physical line.
		n := 0
		for i := len(logical) - 1; i >= 0 && logical[i] == '\\'; i-- {
			n++
		}
		if n%2 != 0 {
			logical = logical[:len(logical)-1]
			continue
		}
		text := strings.TrimSpace(logical)
		logical = ""
		if text == "" || text[0] == '#' || text[0] == ';' {
			continue
		}
		fail := func() ([]ConfigEntry, error) { return nil, fmt.Errorf("malformed git config at line %d", line) }
		if text[0] == '[' {
			end := strings.LastIndexByte(text, ']')
			if end < 0 {
				return fail()
			}
			tail := strings.TrimSpace(text[end+1:])
			if tail != "" && tail[0] != '#' && tail[0] != ';' {
				return fail()
			}
			header := strings.TrimSpace(text[1:end])
			pos := strings.IndexAny(header, " \t")
			subsection = ""
			if pos >= 0 {
				section = strings.ToLower(header[:pos])
				sub := strings.TrimSpace(header[pos:])
				if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
					return fail()
				}
				subsection, err = configValue(sub)
				if err != nil {
					return fail()
				}
			} else {
				section = strings.ToLower(header)
				if p := strings.IndexByte(section, '.'); p >= 0 {
					subsection = section[p+1:]
					section = section[:p]
				}
			}
			if section == "" {
				return fail()
			}
			continue
		}
		if section == "" {
			return fail()
		}
		p := strings.IndexAny(text, "= \t")
		key, value := text, "true"
		if p >= 0 {
			key = text[:p]
			rest := strings.TrimSpace(text[p:])
			if rest == "" || rest[0] == '#' || rest[0] == ';' {
				value = "true"
			} else {
				if rest[0] != '=' {
					return fail()
				}
				value, err = configValue(strings.TrimSpace(rest[1:]))
				if err != nil {
					return fail()
				}
			}
		}
		if key == "" {
			return fail()
		}
		for i, c := range key {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 0 || (c < '0' || c > '9') && c != '-') {
				return fail()
			}
		}
		key = strings.ToLower(key)
		if section == "includeif" {
			return nil, fmt.Errorf("conditional git config includes are unsupported")
		}
		if section == "include" && key == "path" {
			include := value
			if strings.HasPrefix(include, "~/") {
				if home == "" {
					return nil, fmt.Errorf("HOME required for git config include")
				}
				include = filepath.Join(home, include[2:])
			}
			if strings.HasPrefix(include, "~") {
				return nil, fmt.Errorf("named-user git config includes are unsupported")
			}
			if !filepath.IsAbs(include) {
				include = filepath.Join(filepath.Dir(path), include)
			}
			more, err := ReadConfig(include, home, depth+1)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			entries = append(entries, more...)
		} else {
			entries = append(entries, ConfigEntry{section, subsection, key, value})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if logical != "" {
		return nil, fmt.Errorf("unterminated git config continuation")
	}
	return entries, nil
}

func configValue(s string) (string, error) {
	var b strings.Builder
	quoted, escaped, trailing := false, false, 0
	for _, c := range s {
		if escaped {
			switch c {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case '\\', '"':
				b.WriteRune(c)
			default:
				return "", fmt.Errorf("invalid config escape")
			}
			escaped = false
			trailing = 0
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
			trailing = 0
			continue
		}
		if !quoted && (c == '#' || c == ';') {
			break
		}
		b.WriteRune(c)
		if !quoted && (c == ' ' || c == '\t') {
			trailing++
		} else {
			trailing = 0
		}
	}
	if quoted || escaped {
		return "", fmt.Errorf("unterminated config value")
	}
	return b.String()[:b.Len()-trailing], nil
}
