package docsearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"
)

func fixture(readme string) fstest.MapFS {
	return fstest.MapFS{
		"README.md":            {Data: []byte(readme)},
		"skills/ghok/SKILL.md": {Data: []byte("---\nname: ghok\n---\n# Agent skill\nUse ghok.\n")},
	}
}

func loadFixture(t *testing.T, files fstest.MapFS) *Index {
	t.Helper()
	index, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestSectionsAndExactMarkdown(t *testing.T) {
	child := "## Child!\r\nFirst sentence. More prose.\r\n### Nested `item`\r\nNested body.\r\n"
	readme := "# Example\r\nIntroduction.\r\n" + child + "## Last\r\nNo final newline"
	index := loadFixture(t, fixture(readme))
	want := []struct {
		id, title, markdown string
		depth               int
	}{
		{"readme", "Example", readme, 0},
		{"readme/child", "Child!", child, 1},
		{"readme/nested-item", "Nested `item`", "### Nested `item`\r\nNested body.\r\n", 2},
		{"readme/last", "Last", "## Last\r\nNo final newline", 1},
		{"skill", "Agent skill", "# Agent skill\nUse ghok.\n", 0},
	}
	if len(index.Sections) != len(want) {
		t.Fatalf("sections: %+v", index.Sections)
	}
	for n, expected := range want {
		section, ok := index.Lookup(expected.id)
		if !ok || section.ID != expected.id || section.Title != expected.title || section.Depth != expected.depth || section.Markdown != expected.markdown || index.Sections[n].ID != expected.id {
			t.Errorf("Lookup(%q) = %+v, %v; want %+v", expected.id, section, ok, expected)
		}
	}
	if _, ok := index.Lookup("missing"); ok {
		t.Fatal("unknown section was found")
	}
}

func TestAnchorsAndFencedHeadings(t *testing.T) {
	text := "# Hello, *WORLD*!\n## A\n## A\n## A-1\n## A\n## Tokens: `GH_TOKEN` & wait-all? ###\n" +
		"````markdown\n# Ignored\n```\n## Still ignored\n~~~~\n````\n" +
		"~~~ text\n# Also ignored\n~~~\n" +
		"   ```sh\n## Indented fence ignored\n   ```\n" +
		"#### Deep\n## Final\n#not-a-heading\n####### Not a heading\n"
	index := loadFixture(t, fixture(text))
	want := []string{"hello-world", "a", "a-1", "a-1-1", "a-2", "tokens-ghtoken--wait-all", "deep", "final", "agent-skill"}
	for n, section := range index.Sections {
		if n >= len(want) || section.Anchor != want[n] {
			t.Fatalf("section %d = %+v; anchors want %v", n, section, want)
		}
	}
	if len(index.Sections) != len(want) {
		t.Fatalf("got %d sections, want %d", len(index.Sections), len(want))
	}
	if index.Sections[6].Depth != 2 {
		t.Errorf("skipped heading levels must still nest under their parent: %+v", index.Sections[6])
	}
}

func TestDescriptions(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"prose", "\nFirst **sentence** wraps\nto [a link](https://example.com). Second sentence.\n", "First sentence wraps to a link."},
		{"override", "<!-- desc: A precise\n  override. More detail. -->\n\nOther prose.\n", "A precise override. More detail."},
		{"skip blocks", "\n```sh\nNot prose.\n```\n\n| Table |\n| --- |\n| Value |\n\n- List item.\n  Continuation.\n\n1. Numbered item.\n\n<!-- other comment -->\nActual prose! Another sentence.\n", "Actual prose!"},
		{"unbordered table", "\nName | Value\n--- | ---\na | b\n\nActual prose.\n", "Actual prose."},
		{"only blocks", "\n~~~\nNo prose.\n~~~\n\n- Item.\n\n| Table |\n", ""},
		{"version", "\nUse v1.2.3 safely. Next sentence.\n", "Use v1.2.3 safely."},
		{"long", "\n" + strings.Repeat("a", 170), strings.Repeat("a", 159) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := loadFixture(t, fixture("# Example\n"+tc.body))
			got := index.Sections[0].Description
			if got != tc.want || utf8.RuneCountInString(got) > 160 {
				t.Errorf("description = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileOrderAndAllBytes(t *testing.T) {
	files := fixture("# Readme\nNo trailing newline")
	files["docs/z.md"] = &fstest.MapFile{Data: []byte("# Z\r\nLast\r\n")}
	files["docs/a.md"] = &fstest.MapFile{Data: []byte("# A\nFirst\n")}
	files["docs/notes/private.md"] = &fstest.MapFile{Data: []byte("# Private\nNever public\n")}
	files["other.md"] = &fstest.MapFile{Data: []byte("# Other\n")}
	index := loadFixture(t, files)
	wantFiles := []string{"README.md", "docs/a.md", "docs/z.md", "skills/ghok/SKILL.md"}
	var want strings.Builder
	for n, file := range wantFiles {
		if index.Sections[n].File != file {
			t.Fatalf("file order: %+v", index.Sections)
		}
		want.WriteString("<!-- file: " + file + " -->\n")
		want.Write(files[file].Data)
		if n == 0 {
			want.WriteByte('\n')
		}
	}
	if got := index.All(); got != want.String() {
		t.Errorf("All() = %q, want %q", got, want.String())
	}
}

func TestInvalidDocumentsFailLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"missing required file", fstest.MapFS{"README.md": {Data: []byte("# Readme\n")}}, "skills/ghok/SKILL.md"},
		{"empty heading", fixture("# Root\n## \n"), "empty docs title"},
		{"empty closing heading", fixture("# Root\n## ###\n"), "empty docs title"},
		{"missing title", fixture("Only prose.\n"), "missing docs title"},
		{"duplicate id", func() fstest.MapFS {
			files := fixture("# Root\n")
			files["docs/readme.md"] = &fstest.MapFile{Data: []byte("# Duplicate\n")}
			return files
		}(), "duplicate docs id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(tc.files); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSectionJSONOmitsInternalContent(t *testing.T) {
	section := loadFixture(t, fixture("# Root\nProse.\n")).Sections[0]
	data, err := json.Marshal(section)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "readme", "title": "Root", "description": "Prose.", "depth": float64(0), "file": "README.md"}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("section JSON = %s", data)
	}
}
