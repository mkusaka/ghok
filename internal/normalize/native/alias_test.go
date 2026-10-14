package native

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestExpandAlias(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		aliases  map[string]string
		expanded []string
		alias    string
		reason   model.UnknownReason
	}{
		{
			name: "empty invocation",
		},
		{
			name: "not an alias", argv: []string{"other", "arg"},
			aliases: map[string]string{"a": "pr list"}, expanded: []string{"other", "arg"},
		},
		{
			name: "plain", argv: []string{"a"}, aliases: map[string]string{"a": "pr status"},
			expanded: []string{"pr", "status"}, alias: "a",
		},
		{
			name: "extra arguments stay literal", argv: []string{"a", "two words", "'quote'", "$1"},
			aliases:  map[string]string{"a": "pr checkout"},
			expanded: []string{"pr", "checkout", "two words", "'quote'", "$1"}, alias: "a",
		},
		{
			name: "quoted words", argv: []string{"a"},
			aliases:  map[string]string{"a": `issue create --title "two words" --body 'single quotes'`},
			expanded: []string{"issue", "create", "--title", "two words", "--body", "single quotes"}, alias: "a",
		},
		{
			name: "numbered substitution and extras", argv: []string{"a", "alice", "help wanted", "-R", "o/r"},
			aliases:  map[string]string{"a": `issue list --author="$1" --label="$2"`},
			expanded: []string{"issue", "list", "--author=alice", "--label=help wanted", "-R", "o/r"}, alias: "a",
		},
		{
			name: "unquoted substitution splits", argv: []string{"a", "two words"},
			aliases: map[string]string{"a": "api $1"}, expanded: []string{"api", "two", "words"}, alias: "a",
		},
		{
			name: "single quoted substitution", argv: []string{"a", "two words"},
			aliases: map[string]string{"a": "api '$1'"}, expanded: []string{"api", "two words"}, alias: "a",
		},
		{
			name: "replacement happens before quote parsing", argv: []string{"a", "'"},
			aliases: map[string]string{"a": "api '$1'"}, alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "missing argument", argv: []string{"a", "alice"},
			aliases: map[string]string{"a": "issue list --author=$1 --label=$2"}, alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "missing argument in comment", argv: []string{"a"},
			aliases: map[string]string{"a": "api user # $1"}, alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "escaped missing argument still errors", argv: []string{"a"},
			aliases: map[string]string{"a": `api \$1`}, alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "zero placeholder errors", argv: []string{"a", "arg"},
			aliases: map[string]string{"a": "api $0"}, alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "literal dollar discards all arguments", argv: []string{"a", "one", "two"},
			aliases: map[string]string{"a": "api $HOME"}, expanded: []string{"api", "$HOME"}, alias: "a",
		},
		{
			name: "trailing dollar discards arguments", argv: []string{"a", "one"},
			aliases: map[string]string{"a": "api cost$"}, expanded: []string{"api", "cost$"}, alias: "a",
		},
		{
			name: "dollar in supplied argument discards extras", argv: []string{"a", "$coolmoney$", "discarded"},
			aliases:  map[string]string{"a": `api "$1" "$1"`},
			expanded: []string{"api", "$coolmoney$", "$coolmoney$"}, alias: "a",
		},
		{
			name: "replacement introduces next placeholder", argv: []string{"a", "$2", "user", "extra"},
			aliases: map[string]string{"a": "api $1 $2"}, expanded: []string{"api", "user", "user", "extra"}, alias: "a",
		},
		{
			name: "replacement introduces past placeholder", argv: []string{"a", "$1"},
			aliases: map[string]string{"a": "api $1"}, alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "one replaces prefix of ten", argv: []string{"a", "user", "extra"},
			aliases: map[string]string{"a": "api $10"}, expanded: []string{"api", "user0", "extra"}, alias: "a",
		},
		{
			name: "unused earlier argument discarded", argv: []string{"a", "discarded", "user", "extra"},
			aliases: map[string]string{"a": "api $2"}, expanded: []string{"api", "user", "extra"}, alias: "a",
		},
		{
			name: "double dollar is not shell substitution", argv: []string{"a", "user", "discarded"},
			aliases: map[string]string{"a": "api $$1"}, expanded: []string{"api", "$user"}, alias: "a",
		},
		{
			name: "unicode digit is not missing placeholder", argv: []string{"a", "discarded"},
			aliases: map[string]string{"a": "api $１"}, expanded: []string{"api", "$１"}, alias: "a",
		},
		{
			name: "comments after substitution", argv: []string{"a", "user # discarded", "extra"},
			aliases: map[string]string{"a": "api $1"}, expanded: []string{"api", "user", "extra"}, alias: "a",
		},
		{
			name: "shell", argv: []string{"a", "arg"}, aliases: map[string]string{"a": "!echo $1"},
			alias: "a", reason: model.UnknownShellAlias,
		},
		{
			name: "recursive shell", argv: []string{"a"}, aliases: map[string]string{"a": "b", "b": "!echo hi"},
			alias: "a", reason: model.UnknownShellAlias,
		},
		{
			name: "recursive api", argv: []string{"a", "user", "--paginate"},
			aliases:  map[string]string{"a": "b", "b": "api $1"},
			expanded: []string{"api", "user", "--paginate"}, alias: "a",
		},
		{
			name: "direct cycle", argv: []string{"a"}, aliases: map[string]string{"a": "a"},
			alias: "a", reason: model.UnknownAliasLoop,
		},
		{
			name: "indirect cycle", argv: []string{"a"}, aliases: map[string]string{"a": "b", "b": "a"},
			alias: "a", reason: model.UnknownAliasLoop,
		},
		{
			name: "growing cycle hits depth guard", argv: []string{"a"}, aliases: map[string]string{"a": "a more"},
			alias: "a", reason: model.UnknownAliasLoop,
		},
		{
			name: "revisited alias with changed arguments can terminate", argv: []string{"a", "a", "api"},
			aliases: map[string]string{"a": "$1", "$1": "api user"}, expanded: []string{"api"}, alias: "a",
		},
		{
			name: "builtin cannot be shadowed", argv: []string{"pr", "list"},
			aliases: map[string]string{"pr": "!echo unsafe"}, expanded: []string{"pr", "list"},
		},
		{
			name: "api cannot be shadowed", argv: []string{"api", "user"},
			aliases: map[string]string{"api": "a", "a": "api"}, expanded: []string{"api", "user"},
		},
		{
			name: "recursive builtin cannot be shadowed", argv: []string{"a", "user"},
			aliases: map[string]string{"a": "api", "api": "!echo unsafe"}, expanded: []string{"api", "user"}, alias: "a",
		},
		{
			name: "only first argument expands", argv: []string{"issue", "a"},
			aliases: map[string]string{"a": "create"}, expanded: []string{"issue", "a"},
		},
		{
			name: "does not search past root flags", argv: []string{"--help", "a"},
			aliases: map[string]string{"a": "api user"}, expanded: []string{"--help", "a"},
		},
		{
			name: "nested alias is not expanded", argv: []string{"pr", "a"},
			aliases: map[string]string{"pr a": "pr list"}, expanded: []string{"pr", "a"},
		},
		{
			name: "malformed single quote", argv: []string{"a"}, aliases: map[string]string{"a": "api 'user"},
			alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "malformed double quote", argv: []string{"a"}, aliases: map[string]string{"a": `api "user`},
			alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "trailing escape", argv: []string{"a"}, aliases: map[string]string{"a": `api user\`},
			alias: "a", reason: model.UnknownParseError,
		},
		{
			name: "empty expansion matches shlex", argv: []string{"a"}, aliases: map[string]string{"a": ""},
			expanded: []string{}, alias: "a",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := slices.Clone(c.argv)
			expanded, alias, reason := ExpandAlias(c.argv, c.aliases)
			if !reflect.DeepEqual(expanded, c.expanded) || alias != c.alias || reason != c.reason {
				t.Fatalf("got (%#v, %q, %q), want (%#v, %q, %q)", expanded, alias, reason, c.expanded, c.alias, c.reason)
			}
			if !reflect.DeepEqual(c.argv, before) {
				t.Fatal("ExpandAlias mutated input argv")
			}
		})
	}
}

func TestExpandAliasDepth(t *testing.T) {
	for _, depth := range []int{maxAliasDepth, maxAliasDepth + 1} {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			aliases := make(map[string]string, depth)
			for i := range depth {
				aliases["a"+strconv.Itoa(i)] = "a" + strconv.Itoa(i+1)
			}
			aliases["a"+strconv.Itoa(depth-1)] = "api user"
			expanded, alias, reason := ExpandAlias([]string{"a0"}, aliases)
			if alias != "a0" {
				t.Fatalf("alias = %q", alias)
			}
			if depth == maxAliasDepth {
				if reason != model.UnknownNone || !reflect.DeepEqual(expanded, []string{"api", "user"}) {
					t.Fatalf("got (%#v, %q)", expanded, reason)
				}
			} else if reason != model.UnknownAliasLoop || expanded != nil {
				t.Fatalf("got (%#v, %q), want depth limit", expanded, reason)
			}
		})
	}
}

func TestSplitAlias(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
		ok    bool
	}{
		{"empty", "", []string{}, true},
		{"ascii whitespace", " a\tb\rc\nd ", []string{"a", "b", "c", "d"}, true},
		{"nonascii whitespace stays literal", "a\u00a0b\vc\fd", []string{"a\u00a0b\vc\fd"}, true},
		{"adjacent quotes", `a"b c"'d e'f`, []string{"ab cd ef"}, true},
		{"empty quoted words", `'' "" x`, []string{"", "", "x"}, true},
		{"comment between words", "a # 'unterminated \\\n b", []string{"a", "b"}, true},
		{"hash within word", "a#b c", []string{"a#b", "c"}, true},
		{"hash after quote stays in word", `""#literal`, []string{"#literal"}, true},
		{"comment at end", "a # ignored", []string{"a"}, true},
		{"escaped whitespace and hash", `a\ b \#c`, []string{"a b", "#c"}, true},
		{"double quote escapes any rune", `"a\qb\$c\"d\\e"`, []string{`aqb$c"d\e`}, true},
		{"single quote preserves escapes", `'a\b"c#d'`, []string{`a\b"c#d`}, true},
		{"escaped newline is literal", "a\\\nb", []string{"a\nb"}, true},
		{"operators are plain text", "a;b | $(x) && y", []string{"a;b", "|", "$(x)", "&&", "y"}, true},
		{"unicode", "日本語 'é 🚀'", []string{"日本語", "é 🚀"}, true},
		{"invalid utf8 matches ReadRune", "a\xffb", []string{"a\ufffdb"}, true},
		{"single quote eof", "'a", nil, false},
		{"double quote eof", `"a`, nil, false},
		{"escape eof", `a\`, nil, false},
		{"quoted escape eof", `"a\`, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := splitAlias(c.input)
			if ok != c.ok || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got (%#v, %t), want (%#v, %t)", got, ok, c.want, c.ok)
			}
		})
	}
}
