package native

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestNormalizeTexts(t *testing.T) {
	inline := func(value string) *model.Text {
		return &model.Text{Value: value, Source: "inline", Bytes: int64(len(value))}
	}
	unknown := func(source string) *model.Text { return &model.Text{Source: source} }
	for _, tt := range []struct {
		name string
		argv []string
		want *model.Texts
	}{
		{name: "bundled draft body", argv: []string{"pr", "create", "-db", "text", "-tTitle"}, want: &model.Texts{Title: inline("Title"), Body: inline("text")}},
		{name: "long equals empty", argv: []string{"pr", "edit", "7", "--title=", "--body="}, want: &model.Texts{Title: inline(""), Body: inline("")}},
		{name: "issue create", argv: []string{"issue", "create", "--title=Title", "--body=本文"}, want: &model.Texts{Title: inline("Title"), Body: inline("本文")}},
		{name: "issue edit", argv: []string{"issue", "edit", "7", "-btext"}, want: &model.Texts{Body: inline("text")}},
		{name: "pr comment", argv: []string{"pr", "comment", "7", "--body=text"}, want: &model.Texts{Body: inline("text")}},
		{name: "issue comment", argv: []string{"issue", "comment", "7", "-b", ""}, want: &model.Texts{Body: inline("")}},
		{name: "review", argv: []string{"pr", "review", "7", "-abtext"}, want: &model.Texts{Body: inline("text")}},
		{name: "merge", argv: []string{"pr", "merge", "7", "--subject=Subject", "-btext"}, want: &model.Texts{Title: inline("Subject"), Body: inline("text")}},
		{name: "issue close", argv: []string{"issue", "close", "7", "--comment=Closed"}, want: &model.Texts{Body: inline("Closed")}},
		{name: "issue reopen", argv: []string{"issue", "reopen", "7", "-c", ""}, want: &model.Texts{Body: inline("")}},
		{name: "pr close", argv: []string{"pr", "close", "7", "--comment=Closed"}, want: &model.Texts{Body: inline("Closed")}},
		{name: "pr reopen", argv: []string{"pr", "reopen", "7", "-c", "Reopened"}, want: &model.Texts{Body: inline("Reopened")}},
		{name: "release create", argv: []string{"release", "create", "v1", "-tTitle", "--notes=Notes"}, want: &model.Texts{Title: inline("Title"), Notes: inline("Notes")}},
		{name: "release edit", argv: []string{"release", "edit", "v1", "-n", ""}, want: &model.Texts{Notes: inline("")}},
		{name: "fill", argv: []string{"pr", "create", "--fill"}, want: &model.Texts{Title: unknown("fill"), Body: unknown("fill")}},
		{name: "fill first", argv: []string{"pr", "create", "--fill-first"}, want: &model.Texts{Title: unknown("fill"), Body: unknown("fill")}},
		{name: "fill verbose explicit title", argv: []string{"pr", "create", "--fill-verbose", "-tTitle"}, want: &model.Texts{Title: inline("Title"), Body: unknown("fill")}},
		{name: "explicit overrides fill", argv: []string{"pr", "create", "--fill", "--title=", "--body="}, want: &model.Texts{Title: inline(""), Body: inline("")}},
		{name: "fill false", argv: []string{"pr", "create", "--fill=false"}, want: &model.Texts{Title: unknown("editor"), Body: unknown("editor")}},
		{name: "default issue create", argv: []string{"issue", "create"}, want: &model.Texts{Title: unknown("editor"), Body: unknown("editor")}},
		{name: "default comment", argv: []string{"pr", "comment", "7"}, want: &model.Texts{Body: unknown("editor")}},
		{name: "default review", argv: []string{"pr", "review", "7"}, want: &model.Texts{Body: unknown("editor")}},
		{name: "comment review", argv: []string{"pr", "review", "7", "--comment"}, want: &model.Texts{Body: unknown("editor")}},
		{name: "approval without body", argv: []string{"pr", "review", "7", "--approve"}},
		{name: "editor overrides initial text", argv: []string{"issue", "create", "--editor", "-tTitle", "-bInitial"}, want: &model.Texts{Title: unknown("editor"), Body: unknown("editor")}},
		{name: "web comment", argv: []string{"issue", "comment", "7", "--web"}, want: &model.Texts{Body: unknown("editor")}},
		{name: "template", argv: []string{"pr", "create", "-tTitle", "--template=template.md"}, want: &model.Texts{Title: inline("Title"), Body: &model.Text{Source: "template", Path: "template.md"}}},
		{name: "generated release notes", argv: []string{"release", "create", "v1", "--generate-notes", "-nPrefix"}, want: &model.Texts{Notes: unknown("fill")}},
		{name: "tag release notes", argv: []string{"release", "create", "v1", "--notes-from-tag"}, want: &model.Texts{Notes: unknown("fill")}},
		{name: "read command", argv: []string{"pr", "view", "7"}},
		{name: "delete comment", argv: []string{"issue", "comment", "7", "--delete-last", "--yes"}},
		{name: "metadata edit", argv: []string{"issue", "edit", "7", "--add-label=bug"}},
		{name: "close without comment", argv: []string{"issue", "close", "7"}},
		{name: "release default", argv: []string{"release", "create", "v1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := Normalize(model.Invocation{}, tt.argv).Operations[0]
			if !reflect.DeepEqual(op.Texts, tt.want) {
				t.Fatalf("texts=%+v want=%+v", op.Texts, tt.want)
			}
		})
	}
}

func TestNormalizeTextFiles(t *testing.T) {
	for _, tt := range []struct {
		name       string
		argv       []string
		inv        model.Invocation
		want       model.Text
		field      string
		unresolved bool
	}{
		{name: "file", argv: []string{"pr", "edit", "7", "-Fbody.md"}, inv: model.Invocation{ReadFile: func(path string) ([]byte, error) {
			if path != "body.md" {
				t.Fatalf("path=%q", path)
			}
			return []byte("本文\n"), nil
		}}, want: model.Text{Value: "本文\n", Source: "file", Path: "body.md", Bytes: 7}, field: "body"},
		{name: "captured stdin", argv: []string{"pr", "comment", "7", "--body-file=-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte("body\n")}}, want: model.Text{Value: "body\n", Source: "stdin", Bytes: 5}, field: "body"},
		{name: "captured empty stdin", argv: []string{"pr", "review", "7", "-F", "-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured}}, want: model.Text{Source: "stdin"}, field: "body"},
		{name: "uncaptured stdin ignores bytes", argv: []string{"issue", "edit", "7", "-F", "-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinNotCaptured, Data: []byte("unavailable")}}, want: model.Text{Source: "stdin", Status: model.StdinNotCaptured}, field: "body", unresolved: true},
		{name: "oversized stdin", argv: []string{"pr", "merge", "7", "-F", "-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinTooLarge}}, want: model.Text{Source: "stdin", Status: model.StdinTooLarge, Truncated: true}, field: "body", unresolved: true},
		{name: "terminal stdin", argv: []string{"pr", "edit", "7", "-F", "-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinTTY}}, want: model.Text{Source: "stdin", Status: model.StdinTTY}, field: "body", unresolved: true},
		{name: "timed out stdin", argv: []string{"pr", "edit", "7", "-F", "-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinTimeout, Data: []byte("partial")}}, want: model.Text{Source: "stdin", Status: model.StdinTimeout}, field: "body", unresolved: true},
		{name: "stdin read error", argv: []string{"pr", "edit", "7", "-F", "-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinError}}, want: model.Text{Source: "stdin", Status: model.StdinError}, field: "body", unresolved: true},
		{name: "missing file reader", argv: []string{"issue", "comment", "7", "--body-file=missing.md"}, want: model.Text{Source: "file", Path: "missing.md"}, field: "body", unresolved: true},
		{name: "failed file", argv: []string{"pr", "edit", "7", "-Fmissing.md"}, inv: model.Invocation{ReadFile: func(string) ([]byte, error) { return []byte("partial"), errors.New("not readable") }}, want: model.Text{Source: "file", Path: "missing.md"}, field: "body", unresolved: true},
		{name: "oversized file", argv: []string{"pr", "edit", "7", "-Fbig.md"}, inv: model.Invocation{ReadFile: func(string) ([]byte, error) { return nil, errors.New("input too large: file exceeds 8 bytes") }}, want: model.Text{Source: "file", Path: "big.md", Truncated: true}, field: "body", unresolved: true},
		{name: "release notes file", argv: []string{"release", "create", "v1", "--notes-file=notes.md"}, inv: model.Invocation{ReadFile: func(string) ([]byte, error) { return []byte("notes"), nil }}, want: model.Text{Value: "notes", Source: "file", Path: "notes.md", Bytes: 5}, field: "notes"},
		{name: "release notes stdin", argv: []string{"release", "edit", "v1", "-F-"}, inv: model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte("notes")}}, want: model.Text{Value: "notes", Source: "stdin", Bytes: 5}, field: "notes"},
		{name: "unavailable release notes", argv: []string{"release", "edit", "v1", "-F-"}, want: model.Text{Source: "stdin", Status: model.StdinNotCaptured}, field: "notes", unresolved: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := Normalize(tt.inv, tt.argv).Operations[0]
			if op.Texts == nil {
				t.Fatal("missing texts")
			}
			text := op.Texts.Body
			if tt.field == "notes" {
				text = op.Texts.Notes
			}
			if text == nil || *text != tt.want {
				t.Fatalf("text=%+v want=%+v", text, tt.want)
			}
			found := false
			for _, u := range op.Unresolved {
				if u.Field == "texts."+tt.field {
					found = true
					if u.Source != tt.want.Source {
						t.Fatalf("unresolved=%+v", u)
					}
				}
			}
			if found != tt.unresolved {
				t.Fatalf("unresolved=%+v", op.Unresolved)
			}
		})
	}
}

func TestNormalizeTextsPreservesParameters(t *testing.T) {
	op := Normalize(model.Invocation{}, []string{"pr", "merge", "7", "-db", "text", "-tSubject"}).Operations[0]
	if op.Parameters["body"] != true || op.Parameters["subject"] != "Subject" || op.Parameters["deleteBranch"] != true {
		t.Fatalf("parameters=%+v", op.Parameters)
	}
	if op.Texts == nil || op.Texts.Body.Value != "text" || op.Texts.Title.Value != "Subject" {
		t.Fatalf("texts=%+v", op.Texts)
	}
}

func TestNormalizeTextsDoesNotReadUnusedFiles(t *testing.T) {
	inv := model.Invocation{ReadFile: func(path string) ([]byte, error) {
		t.Fatalf("unexpected read of %q", path)
		return nil, nil
	}}
	for _, argv := range [][]string{
		{"pr", "create", "--body=", "--body-file=unused", "--template=unused"},
		{"issue", "create", "--editor", "--body-file=unused"},
		{"release", "create", "v1", "--generate-notes", "--notes-file=unused"},
		{"pr", "create", "--template=unused"},
	} {
		op := Normalize(inv, argv).Operations[0]
		for _, u := range op.Unresolved {
			if strings.HasPrefix(u.Field, "texts.") && u.Source == "file" {
				t.Fatalf("unexpected unresolved file: %+v", u)
			}
		}
	}
}
