// Package docsearch indexes the documentation shipped with ghok.
package docsearch

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"unicode"
)

type Section struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Depth       int    `json:"depth"`
	File        string `json:"file"`
	Markdown    string `json:"-"`
	Anchor      string `json:"-"`
}

type Index struct {
	Sections  []Section
	byID      map[string]int
	all       string
	documents []document
	frequency map[string]int
	average   float64
}

type heading struct {
	start, body, level int
	title, anchor      string
}

// Load reads only the public documentation, in its displayed file order.
func Load(fsys fs.FS) (*Index, error) {
	docs, err := fs.Glob(fsys, "docs/*.md")
	if err != nil {
		return nil, err
	}
	files := append([]string{"README.md"}, docs...)
	files = append(files, "skills/ghok/SKILL.md")
	index := &Index{byID: make(map[string]int), frequency: make(map[string]int)}
	var all strings.Builder
	previousNewline := true
	for _, file := range files {
		data, readErr := fs.ReadFile(fsys, file)
		if readErr != nil {
			return nil, fmt.Errorf("read docs %s: %w", file, readErr)
		}
		text := string(data)
		if !previousNewline {
			all.WriteByte('\n')
		}
		fmt.Fprintf(&all, "<!-- file: %s -->\n", file)
		all.WriteString(text)
		previousNewline = strings.HasSuffix(text, "\n")
		sections, bodies, parseErr := parse(file, text)
		if parseErr != nil {
			return nil, parseErr
		}
		for n, section := range sections {
			if _, exists := index.byID[section.ID]; exists {
				return nil, fmt.Errorf("duplicate docs id %q", section.ID)
			}
			index.byID[section.ID] = len(index.Sections)
			index.Sections = append(index.Sections, section)
			index.addDocument(section, bodies[n])
		}
	}
	index.all = all.String()
	if len(index.documents) > 0 {
		index.average /= float64(len(index.documents))
	}
	return index, nil
}

func (index *Index) Lookup(id string) (Section, bool) {
	n, found := index.byID[id]
	if !found {
		return Section{}, false
	}
	return index.Sections[n], true
}

// All preserves each file's bytes, adding a line boundary between files if needed.
func (index *Index) All() string { return index.all }

func parse(file, text string) ([]Section, []string, error) {
	var headings []heading
	used := make(map[string]bool)
	var fence fenceState
	offset := 0
	for line := range strings.SplitAfterSeq(text, "\n") {
		if fence.consume(line) {
			offset += len(line)
			continue
		}
		level, title, ok := parseHeading(line)
		if ok {
			if title == "" {
				return nil, nil, fmt.Errorf("empty docs title in %s", file)
			}
			base := slug(title)
			anchor := base
			for suffix := 1; used[anchor]; suffix++ {
				anchor = fmt.Sprintf("%s-%d", base, suffix)
			}
			used[anchor] = true
			headings = append(headings, heading{start: offset, body: offset + len(line), level: level, title: title, anchor: anchor})
		}
		offset += len(line)
	}
	if len(headings) == 0 {
		return nil, nil, fmt.Errorf("missing docs title in %s", file)
	}
	fileSlug := strings.ToLower(strings.TrimSuffix(path.Base(file), path.Ext(file)))
	sections := make([]Section, 0, len(headings))
	bodies := make([]string, 0, len(headings))
	var ancestors []int
	for n, h := range headings {
		end := len(text)
		for _, next := range headings[n+1:] {
			if next.level <= h.level {
				end = next.start
				break
			}
		}
		bodyEnd := len(text)
		if n+1 < len(headings) {
			bodyEnd = headings[n+1].start
		}
		body := text[h.body:bodyEnd]
		id := fileSlug + "/" + h.anchor
		if n == 0 {
			id = fileSlug
		}
		for len(ancestors) > 0 && ancestors[len(ancestors)-1] >= h.level {
			ancestors = ancestors[:len(ancestors)-1]
		}
		sections = append(sections, Section{
			ID: id, Title: h.title, Description: description(body), Depth: len(ancestors),
			File: file, Markdown: text[h.start:end], Anchor: h.anchor,
		})
		ancestors = append(ancestors, h.level)
		bodies = append(bodies, body)
	}
	return sections, bodies, nil
}

func parseHeading(line string) (int, string, bool) {
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || (level < len(trimmed) && trimmed[level] != ' ' && trimmed[level] != '\t') {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	withoutClosing := strings.TrimRight(title, "#")
	if len(withoutClosing) < len(title) && (withoutClosing == "" || strings.HasSuffix(withoutClosing, " ") || strings.HasSuffix(withoutClosing, "\t")) {
		title = strings.TrimSpace(withoutClosing)
	}
	return level, title, true
}

// fenceState follows Markdown's matching fence character and minimum length.
// A shorter fence or a fence of another character cannot close a code block.
type fenceState struct {
	character byte
	length    int
}

func (f *fenceState) consume(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) <= 3 && len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
		n := 0
		for n < len(trimmed) && trimmed[n] == trimmed[0] {
			n++
		}
		if f.length > 0 {
			if trimmed[0] == f.character && n >= f.length && strings.TrimSpace(trimmed[n:]) == "" {
				f.length = 0
			}
			return true
		}
		if n >= 3 && (trimmed[0] != '`' || !strings.Contains(trimmed[n:], "`")) {
			f.character, f.length = trimmed[0], n
			return true
		}
	}
	return f.length > 0
}

func slug(title string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), r == '-':
			result.WriteRune(r)
		case unicode.IsSpace(r):
			result.WriteByte('-')
		}
	}
	return result.String()
}

var (
	listLine  = regexp.MustCompile(`^(?:[-+*]\s|[0-9]+[.)]\s)`)
	tableRule = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)+\|?\s*$`)
	linkText  = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	htmlTag   = regexp.MustCompile(`<[^>]*>`)
)

func description(body string) string {
	if after, ok := strings.CutPrefix(body, "<!-- desc:"); ok {
		if override, _, found := strings.Cut(after, "-->"); found {
			return shorten(strings.Join(strings.Fields(override), " "))
		}
	}
	var fence fenceState
	var prose []string
	skipParagraph, comment := false, false
	lines := strings.Split(body, "\n")
	for n, line := range lines {
		if fence.consume(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") {
			comment = true
		}
		if comment {
			if strings.Contains(trimmed, "-->") {
				comment = false
			}
			continue
		}
		if trimmed == "" {
			if len(prose) > 0 {
				break
			}
			skipParagraph = false
			continue
		}
		if n+1 < len(lines) && tableRule.MatchString(lines[n+1]) {
			skipParagraph = true
		}
		if strings.HasPrefix(trimmed, "|") || listLine.MatchString(trimmed) || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			skipParagraph = true
		}
		if !skipParagraph {
			prose = append(prose, trimmed)
		}
	}
	text := strings.Join(prose, " ")
	text = linkText.ReplaceAllString(text, "$1")
	text = htmlTag.ReplaceAllString(text, "")
	text = strings.NewReplacer("`", "", "*", "", "~~", "").Replace(text)
	for n, r := range text {
		if (r == '.' || r == '!' || r == '?') && (n+1 == len(text) || text[n+1] == ' ') {
			text = text[:n+1]
			break
		}
	}
	return shorten(text)
}

func shorten(text string) string {
	runes := []rune(text)
	if len(runes) > 160 {
		return string(runes[:159]) + "…"
	}
	return text
}
