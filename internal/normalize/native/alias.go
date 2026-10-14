package native

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

const maxAliasDepth = 64

// ExpandAlias expands plain aliases at argv[0], including recursive dispatch.
// Built-in commands take priority over configured aliases. The returned alias
// is the first alias expanded; failures return a nil argv and an unknown reason.
func ExpandAlias(argv []string, aliases map[string]string) (expanded []string, alias string, reason model.UnknownReason) {
	expanded = argv
	var seen [][]string
	for len(expanded) > 0 && !IsBuiltinCommand(expanded[0]) {
		expansion, ok := aliases[expanded[0]]
		if !ok {
			break
		}
		if alias == "" {
			alias = expanded[0]
		}
		if len(seen) >= maxAliasDepth {
			return nil, alias, model.UnknownAliasLoop
		}
		for _, previous := range seen {
			if slices.Equal(previous, expanded) {
				return nil, alias, model.UnknownAliasLoop
			}
		}
		seen = append(seen, expanded)
		if strings.HasPrefix(expansion, "!") {
			return nil, alias, model.UnknownShellAlias
		}

		// Match cli/cli's expandAlias literally: any dollar suppresses extra
		// arguments, even when it is not a numbered placeholder. Replacement
		// precedes lexing, including substitution inside quotes and comments.
		var extra []string
		for i, arg := range expanded[1:] {
			if !strings.Contains(expansion, "$") {
				extra = expanded[i+1:]
				break
			} else {
				expansion = strings.ReplaceAll(expansion, "$"+strconv.Itoa(i+1), arg)
			}
		}
		for i := range len(expansion) {
			if expansion[i] == '$' && i+1 < len(expansion) && expansion[i+1] >= '0' && expansion[i+1] <= '9' {
				return nil, alias, model.UnknownParseError
			}
		}
		var okSplit bool
		expanded, okSplit = splitAlias(expansion)
		if !okSplit {
			return nil, alias, model.UnknownParseError
		}
		expanded = append(expanded, extra...)
	}
	return expanded, alias, model.UnknownNone
}

// splitAlias mirrors google/shlex at e7afc7fbc510, the version used by gh
// v2.102.0. Only ASCII space/tab/CR/LF separate words. A comment starts only
// between words; double quotes escape every following rune, unlike a shell.
func splitAlias(input string) ([]string, bool) {
	const (
		start = iota
		word
		singleQuote
		doubleQuote
		escape
		quotedEscape
		comment
	)
	state := start
	words := make([]string, 0)
	var token strings.Builder
	for _, r := range input {
		switch state {
		case comment:
			if r == '\n' {
				state = start
			}
		case escape, quotedEscape:
			token.WriteRune(r)
			if state == escape {
				state = word
			} else {
				state = doubleQuote
			}
		case singleQuote:
			if r == '\'' {
				state = word
			} else {
				token.WriteRune(r)
			}
		case doubleQuote:
			switch r {
			case '"':
				state = word
			case '\\':
				state = quotedEscape
			default:
				token.WriteRune(r)
			}
		case start, word:
			switch r {
			case ' ', '\t', '\r', '\n':
				if state == word {
					words = append(words, token.String())
					token.Reset()
					state = start
				}
			case '\'':
				state = singleQuote
			case '"':
				state = doubleQuote
			case '\\':
				state = escape
			case '#':
				if state == start {
					state = comment
				} else {
					token.WriteRune(r)
				}
			default:
				token.WriteRune(r)
				state = word
			}
		}
	}
	if state != start && state != word && state != comment {
		return nil, false
	}
	if state == word {
		words = append(words, token.String())
	}
	return words, true
}
