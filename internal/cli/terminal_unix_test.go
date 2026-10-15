//go:build unix

package cli

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTerminalTextEscapes(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"review\x1b[31m\r\a\x7f\u0085", `review\x1b[31m\x0d\x07\x7f\x85`},
		{"first\r\n\tsecond", "first\\x0d\n\tsecond"},
		{"invalid\xff\xc0 UTF-8 \ufffd", `invalid\xff\xc0 UTF-8 ` + "\ufffd"},
		{"", ""},
	} {
		if got := terminalText(tc.input); got != tc.want {
			t.Errorf("terminalText(%q)=%q, want %q", tc.input, got, tc.want)
		}
	}
	for r := rune(0); r <= 0x9f; r++ {
		if r >= 0x20 && r < 0x7f || r == '\n' || r == '\t' {
			continue
		}
		if got, want := terminalText(string(r)), fmt.Sprintf(`\x%02x`, r); got != want {
			t.Errorf("control %U: got %q, want %q", r, got, want)
		}
	}
}

func TestTerminalTextLimits(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{strings.Repeat("x", 500), strings.Repeat("x", 500)},
		{strings.Repeat("x", 501), strings.Repeat("x", 499) + "…"},
		{strings.Repeat("界", 501), strings.Repeat("界", 499) + "…"},
		{strings.Repeat("x", 498) + "\x1b", strings.Repeat("x", 498) + "…"},
		{strings.Repeat("x\n", 39) + "x", strings.Repeat("x\n", 39) + "x"},
		{strings.Repeat("x\n", 99) + "x", strings.Repeat("x\n", 39) + "    … (61 more lines truncated)"},
		{strings.Repeat(strings.Repeat("界", 400)+"\n", 9) + "end", strings.Repeat(strings.Repeat("界", 400)+"\n", 3) + "    … (7 more lines truncated)"},
	} {
		got := terminalText(tc.input)
		if got != tc.want {
			t.Errorf("terminalText: got %q, want %q", got, tc.want)
		}
		if len(got)+1 > 4<<10 || strings.Count(got, "\n")+1 > 40 || !utf8.ValidString(got) {
			t.Errorf("terminal limits violated: bytes=%d, lines=%d, valid=%t", len(got)+1, strings.Count(got, "\n")+1, utf8.ValidString(got))
		}
		for _, line := range strings.Split(got, "\n") {
			if utf8.RuneCountInString(line) > 500 {
				t.Errorf("line exceeds 500 characters: %q", line)
			}
		}
	}
}
