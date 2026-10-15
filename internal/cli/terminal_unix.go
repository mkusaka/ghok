//go:build unix

package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// terminalText bounds one redacted display entry, including its formatting and
// the newline added by the caller. Never apply it to protocol or log values.
func terminalText(text string) string {
	const maxLines, maxBytes = 40, 4 << 10
	var storage [maxLines]string
	lines := storage[:0]
	size := 0
	remaining := strings.Count(text, "\n") + 1
	markerBytes := len("    … ( more lines truncated)\n")
	for n := remaining; n > 0; n /= 10 {
		markerBytes++
	}
	for ; remaining > 0; remaining-- {
		line, rest, _ := strings.Cut(text, "\n")
		line = terminalLine(line)
		reserve := 0
		if remaining > 1 {
			reserve = markerBytes
		}
		if len(lines) == maxLines-1 && remaining > 1 || size+len(line)+1+reserve > maxBytes {
			lines = append(lines, fmt.Sprintf("    … (%d more lines truncated)", remaining))
			break
		}
		lines = append(lines, line)
		size += len(line) + 1
		text = rest
	}
	if len(lines) == 1 {
		return lines[0]
	}
	return strings.Join(lines, "\n")
}

func terminalLine(line string) string {
	const maxCharacters = 500
	const hex = "0123456789abcdef"
	var output strings.Builder
	start, characters := 0, 0
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		invalid := r == utf8.RuneError && size == 1
		escape := invalid || r < 0x20 && r != '\t' || r >= 0x7f && r <= 0x9f
		width := 1
		if escape {
			width = 4 // Visible \xNN, counted as four displayed characters.
		}
		if characters+width > maxCharacters || characters+width == maxCharacters && i+size < len(line) {
			output.WriteString(line[start:i])
			output.WriteRune('…')
			return output.String()
		}
		if escape {
			output.WriteString(line[start:i])
			value := byte(r)
			if invalid {
				value = line[i]
			}
			output.WriteString(`\x`)
			output.WriteByte(hex[value>>4])
			output.WriteByte(hex[value&0xf])
			start = i + size
		}
		characters += width
		i += size
	}
	if start == 0 {
		return line
	}
	output.WriteString(line[start:])
	return output.String()
}
