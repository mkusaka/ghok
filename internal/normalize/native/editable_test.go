package native

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestGeneratedEditableFlagMetadata(t *testing.T) {
	for path, c := range generatedCommands {
		for _, f := range c.flags {
			parts := strings.Split(f.long, "-")
			for i := 1; i < len(parts); i++ {
				if parts[i] != "" {
					parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
				}
			}
			if want := strings.Join(parts, ""); f.paramKey != want {
				t.Errorf("%s --%s parameter key = %q, want %q", path, f.long, f.paramKey, want)
			}
			if f.editable && f.valueType != "bool" && f.valueType != "string" && !f.repeatable() {
				t.Errorf("unsupported editable type: %s --%s: %s", path, f.long, f.valueType)
			}
			switch f.long {
			case "auto", "disable-auto", "admin", "merge", "squash", "rebase", "approve", "request-changes", "edit-last", "delete-last", "editor", "web", "fill", "fill-first", "fill-verbose", "template", "generate-notes", "notes-from-tag", "no-store":
				if f.editable {
					t.Errorf("identity/generated-input flag is editable: %s --%s", path, f.long)
				}
			}
		}
	}
	for _, tt := range []struct{ path, flag, key string }{
		{"pr create", "label", "label"},
		{"pr create", "assignee", "assignee"},
		{"pr create", "reviewer", "reviewer"},
		{"pr create", "base", "base"},
		{"pr create", "draft", "draft"},
		{"pr edit", "add-reviewer", "addReviewer"},
		{"issue edit", "remove-label", "removeLabel"},
		{"issue edit", "milestone", "milestone"},
		{"release edit", "prerelease", "prerelease"},
	} {
		f, ok := lookupFlag(registry[tt.path], tt.flag, 0)
		if !ok || !f.editable || f.paramKey != tt.key {
			t.Errorf("missing curated flag metadata: %+v: %+v", tt, f)
		}
	}
}

func TestNativeEditableArgvSpans(t *testing.T) {
	for _, tt := range []struct {
		name                                   string
		argv                                   []string
		path, flag, parameter, text, valueType string
		spans                                  []model.ArgSpan
	}{
		{"long equals repeats", []string{"pr", "edit", "4", "--body=old", "--body", "new", "--body=new"}, "texts/body/value", "body", "body", "body", "string", []model.ArgSpan{{Start: 3, End: 4}, {Start: 4, End: 6}, {Start: 6, End: 7}}},
		{"attached short", []string{"pr", "edit", "4", "-btext"}, "texts/body/value", "body", "body", "body", "string", []model.ArgSpan{{Start: 3, End: 4}}},
		{"short equals", []string{"pr", "edit", "4", "-b=text"}, "texts/body/value", "body", "body", "body", "string", []model.ArgSpan{{Start: 3, End: 4}}},
		{"bundle attached", []string{"pr", "create", "-ddbtwo"}, "texts/body/value", "body", "body", "body", "string", []model.ArgSpan{{Start: 2, End: 3, Keep: []string{"-d", "-d"}, KeepGroups: [][]string{{"-d"}, {"-d"}}}}},
		{"bundle separate", []string{"pr", "create", "-db", "two"}, "texts/body/value", "body", "body", "body", "string", []model.ArgSpan{{Start: 2, End: 4, Keep: []string{"-d"}, KeepGroups: [][]string{{"-d"}}}}},
		{"duplicate bool bundle", []string{"pr", "create", "-ddbtwo", "--draft=false", "-d"}, "parameters/draft", "draft", "draft", "", "bool", []model.ArgSpan{{Start: 2, End: 3, Keep: []string{"-btwo"}, KeepGroups: [][]string{{"-btwo"}}}, {Start: 3, End: 4}, {Start: 4, End: 5}}},
		{"bool keeps separate text", []string{"pr", "create", "-db", "two"}, "parameters/draft", "draft", "draft", "", "bool", []model.ArgSpan{{Start: 2, End: 4, Keep: []string{"-b", "two"}, KeepGroups: [][]string{{"-b", "two"}}}}},
		{"repeatable duplicates", []string{"pr", "create", "--label=x,x", "-lx", "--label", "x"}, "parameters/label", "label", "label", "", "stringSlice", []model.ArgSpan{{Start: 2, End: 3}, {Start: 3, End: 4}, {Start: 4, End: 6}}},
		{"expanded root and parent flags", []string{"--repo=a/b", "pr", "-Ra/b", "edit", "4", "--repo", "a/b"}, "target/repository", "repo", "", "", "string", []model.ArgSpan{{Start: 0, End: 1}, {Start: 2, End: 3}, {Start: 5, End: 7}}},
		{"built-in alias indexes", []string{"pr", "new", "-tTitle", "-bBody"}, "texts/title/value", "title", "title", "title", "string", []model.ArgSpan{{Start: 2, End: 3}}},
		{"merge subject", []string{"pr", "merge", "4", "-dstTitle"}, "texts/title/value", "subject", "subject", "title", "string", []model.ArgSpan{{Start: 3, End: 4, Keep: []string{"-d", "-s"}, KeepGroups: [][]string{{"-d"}, {"-s"}}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := Normalize(model.Invocation{}, tt.argv).Operations[0]
			got, ok := op.Editable[tt.path]
			want := model.EditLocator{Kind: "argv", Flag: tt.flag, Parameter: tt.parameter, Text: tt.text, ValueType: tt.valueType, Spans: tt.spans}
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("locator %s = %+v, want %+v", tt.path, got, want)
			}
		})
	}
}

func TestNativeEditableTextSurfaces(t *testing.T) {
	for _, tt := range []struct{ command, flag, field string }{
		{"pr create", "title", "title"}, {"issue create", "title", "title"},
		{"pr create", "body", "body"}, {"issue create", "body", "body"},
		{"pr edit", "body", "body"}, {"issue edit", "body", "body"},
		{"pr comment", "body", "body"}, {"issue comment", "body", "body"},
		{"pr review", "body", "body"}, {"pr merge", "subject", "title"},
		{"pr merge", "body", "body"}, {"pr close", "comment", "body"},
		{"pr reopen", "comment", "body"}, {"issue close", "comment", "body"},
		{"issue reopen", "comment", "body"}, {"release create", "title", "title"},
		{"release edit", "title", "title"}, {"release create", "notes", "notes"},
		{"release edit", "notes", "notes"},
	} {
		argv := append(strings.Fields(tt.command), "--"+tt.flag+"=")
		op := Normalize(model.Invocation{}, argv).Operations[0]
		loc, ok := op.Editable["texts/"+tt.field+"/value"]
		if !ok || loc.Kind != "argv" || loc.Flag != tt.flag || loc.Text != tt.field || loc.Parameter != tt.flag {
			t.Errorf("%v missing text locator: %+v", argv, loc)
		}
		_, parameterEditable := op.Editable["parameters/"+tt.flag]
		if parameterEditable != (tt.flag != "body") {
			t.Errorf("%v parameter mirror editable=%v", argv, parameterEditable)
		}
	}
	for _, tt := range []struct{ command, flag, field, key string }{
		{"pr create", "body-file", "body", "bodyFile"},
		{"pr edit", "body-file", "body", "bodyFile"},
		{"issue create", "body-file", "body", "bodyFile"},
		{"issue edit", "body-file", "body", "bodyFile"},
		{"pr comment", "body-file", "body", "bodyFile"},
		{"issue comment", "body-file", "body", "bodyFile"},
		{"pr review", "body-file", "body", "bodyFile"},
		{"pr merge", "body-file", "body", "bodyFile"},
		{"release create", "notes-file", "notes", "notesFile"},
		{"release edit", "notes-file", "notes", "notesFile"},
	} {
		for _, source := range []string{"file", "stdin"} {
			path := "message.txt"
			if source == "stdin" {
				path = "-"
			}
			inv := model.Invocation{ReadFile: func(string) ([]byte, error) { return []byte("text"), nil }, Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte("text")}}
			argv := append(strings.Fields(tt.command), "--"+tt.flag, "old", "-F"+path)
			op := Normalize(inv, argv).Operations[0]
			loc, ok := op.Editable["texts/"+tt.field+"/value"]
			want := model.EditLocator{Kind: source, Flag: tt.flag, ValueType: "string", Parameter: tt.key, Text: tt.field, Input: path, Spans: []model.ArgSpan{{Start: 2, End: 4}, {Start: 4, End: 5}}}
			if !ok || !reflect.DeepEqual(loc, want) {
				t.Errorf("%v locator=%+v, want %+v", argv, loc, want)
			}
			if _, ok := op.Editable["parameters/"+tt.key]; ok {
				t.Errorf("file path/presence marker must not be a prose parameter: %v", argv)
			}
		}
	}
}

func TestNativeEditableInsertionsAndTargets(t *testing.T) {
	for _, argv := range [][]string{
		{"pr", "edit", "4"}, {"issue", "edit", "#4"}, {"pr", "merge", "4"},
		{"release", "create", "v1"}, {"release", "edit", "v1"},
	} {
		op := Normalize(model.Invocation{}, argv).Operations[0]
		field := "body"
		if argv[0] == "release" {
			field = "notes"
		}
		for _, path := range []string{"texts/title/value", "texts/" + field + "/value", "target/repository"} {
			loc, ok := op.Editable[path]
			if !ok || loc.Kind != "argv" || len(loc.Spans) != 0 {
				t.Errorf("%v missing insertion locator %s: %+v", argv, path, loc)
			}
		}
		if argv[0] != "release" {
			loc := op.Editable["target/number"]
			if loc.Kind != "positional" || !reflect.DeepEqual(loc.Spans, []model.ArgSpan{{Start: 2, End: 3}}) {
				t.Errorf("%v number locator=%+v", argv, loc)
			}
		}
	}
	for _, argv := range [][]string{{"pr", "edit"}, {"pr", "merge", "--"}, {"issue", "edit"}} {
		op := Normalize(model.Invocation{}, argv).Operations[0]
		if loc, ok := op.Editable["target/number"]; !ok || loc.Kind != "positional" || len(loc.Spans) != 0 {
			t.Errorf("%v missing number insertion locator: %+v", argv, loc)
		}
	}
	for _, argv := range [][]string{{"repo", "edit"}, {"repo", "view", "--"}} {
		op := Normalize(model.Invocation{}, argv).Operations[0]
		if loc, ok := op.Editable["target/repository"]; !ok || loc.Kind != "positional" || len(loc.Spans) != 0 {
			t.Errorf("%v missing repository insertion locator: %+v", argv, loc)
		}
	}
	clone := Normalize(model.Invocation{Env: map[string]string{"GH_REPO": "a/b"}}, []string{"repo", "clone", "--", "--depth", "1"}).Operations[0]
	if _, ok := clone.Editable["target/repository"]; ok {
		t.Error("forwarded git arguments must not become a repository selector")
	}
	for _, argv := range [][]string{{"pr", "edit", "--", "4"}, {"repo", "edit", "a/b"}} {
		op := Normalize(model.Invocation{}, argv).Operations[0]
		path, index := "target/number", 3
		if argv[0] == "repo" {
			path, index = "target/repository", 2
		}
		if got := op.Editable[path]; got.Kind != "positional" || !reflect.DeepEqual(got.Spans, []model.ArgSpan{{Start: index, End: index + 1}}) {
			t.Errorf("%v locator=%+v", argv, got)
		}
	}
}

func TestNativeUnsupportedInputsHaveNoTextLocators(t *testing.T) {
	for _, tt := range []struct {
		argv             []string
		inv              model.Invocation
		field, parameter string
	}{
		{argv: []string{"pr", "create", "--fill"}, field: "body", parameter: "body"},
		{argv: []string{"pr", "create", "--template=x"}, field: "body", parameter: "body"},
		{argv: []string{"pr", "create", "--editor", "--title=T"}, field: "title", parameter: "title"},
		{argv: []string{"issue", "create", "--web", "--title=T"}, field: "title", parameter: "title"},
		{argv: []string{"pr", "comment", "4", "--editor", "--body=B"}, field: "body", parameter: "body"},
		{argv: []string{"release", "create", "v1", "--generate-notes", "--notes=N"}, field: "notes", parameter: "notes"},
		{argv: []string{"release", "create", "v1", "--notes-from-tag", "--notes=N"}, field: "notes", parameter: "notes"},
		{argv: []string{"pr", "edit", "4", "--body-file=missing"}, field: "body", parameter: "bodyFile"},
		{argv: []string{"pr", "edit", "4", "--body-file=-"}, field: "body", parameter: "bodyFile"},
		{argv: []string{"pr", "edit", "4", "--body-file=-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinTooLarge}}, field: "body", parameter: "bodyFile"},
		{argv: []string{"pr", "edit", "4", "--body=B", "--body-file=x"}, field: "body", parameter: "body"},
		{argv: []string{"pr", "comment", "4", "--delete-last"}, field: "body", parameter: "body"},
		{argv: []string{"pr", "create", "--recover=x", "--title=T"}, field: "title", parameter: "title"},
	} {
		op := Normalize(tt.inv, tt.argv).Operations[0]
		for _, path := range []string{"texts/" + tt.field + "/value", "parameters/" + tt.parameter} {
			if loc, ok := op.Editable[path]; ok {
				t.Errorf("%v advertises unsafe %s: %+v", tt.argv, path, loc)
			}
		}
	}
	for _, argv := range [][]string{
		{"pr", "edit", "4", "--future=x"}, {"pr", "edit", "4", "--body"},
	} {
		if op := Normalize(model.Invocation{}, argv).Operations[0]; len(op.Editable) != 0 {
			t.Errorf("uncertain parse has editable locations: %v: %+v", argv, op.Editable)
		}
	}
	for _, argv := range [][]string{{"pr", "edit", "https://github.com/a/b/pull/4"}, {"issue", "edit", "4", "5"}} {
		op := Normalize(model.Invocation{}, argv).Operations[0]
		if _, ok := op.Editable["target/number"]; ok {
			t.Errorf("coupled/multiple selector is editable: %v", argv)
		}
	}
}

func TestNativeLocatorsStayPrivate(t *testing.T) {
	op := Normalize(model.Invocation{}, []string{"pr", "edit", "4", "--body=private prose"}).Operations[0]
	data, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Editable) == 0 || strings.Contains(string(data), "Editable") || strings.Contains(string(data), "Spans") || strings.Contains(string(data), "ValueType") {
		t.Fatalf("private locator contract leaked or absent: %s", data)
	}
}

func FuzzNativeEditableBundlePreservesNeighbours(f *testing.F) {
	f.Add("old", "new", uint8(2))
	f.Add("=old", "--new", uint8(0))
	f.Add("", "", uint8(7))
	f.Fuzz(func(t *testing.T, old, replacement string, repeats uint8) {
		argv := []string{"pr", "create", "--title=T", "-" + strings.Repeat("d", int(repeats%8)+1) + "b" + old, "--body=" + old, "--label=a,a", "--label=a", "--milestone=M"}
		before := Normalize(model.Invocation{}, argv).Operations[0]
		locator, ok := before.Editable["texts/body/value"]
		if !ok {
			t.Fatalf("missing body locator: %+v", before)
		}
		var rewritten []string
		cursor := 0
		for _, span := range locator.Spans {
			if span.Start < cursor || span.End > len(argv) || span.Start >= span.End {
				t.Fatalf("invalid spans: %+v", locator.Spans)
			}
			rewritten = append(rewritten, argv[cursor:span.Start]...)
			rewritten = append(rewritten, span.Keep...)
			cursor = span.End
		}
		rewritten = append(rewritten, argv[cursor:]...)
		rewritten = append(rewritten, "--"+locator.Flag+"="+replacement)
		after := Normalize(model.Invocation{}, rewritten).Operations[0]
		if after.Texts == nil || after.Texts.Body == nil || after.Texts.Body.Value != replacement {
			t.Fatalf("body was not replaced: %q: %+v", replacement, after)
		}
		if !reflect.DeepEqual(before.Parameters, after.Parameters) || !reflect.DeepEqual(before.Texts.Title, after.Texts.Title) || !reflect.DeepEqual(before.Target, after.Target) || before.Action != after.Action {
			t.Fatalf("neighbour changed for repeats=%d: before=%+v after=%+v", repeats, before, after)
		}
	})
}
