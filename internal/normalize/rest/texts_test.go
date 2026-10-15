package rest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestRequestBodyTexts(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want *model.Texts
	}{
		{"pull create", []string{"repos/o/r/pulls", "-f", "title=title", "-f", "body=body"}, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"pull update empty", []string{"-XPATCH", "repos/o/r/pulls/1", "-f", "body="}, &model.Texts{Body: &model.Text{Source: "inline"}}},
		{"issue create", []string{"repos/o/r/issues", "-f", "body=日本語"}, &model.Texts{Body: &model.Text{Value: "日本語", Source: "inline", Bytes: 9}}},
		{"issue close with body", []string{"-XPATCH", "repos/o/r/issues/1", "-f", "state=closed", "-f", "body=body"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"issue comment", []string{"repos/o/r/issues/1/comments", "-f", "body=body"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"comment update", []string{"-XPATCH", "repos/o/r/pulls/comments/1", "-f", "body=body"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"review create", []string{"repos/o/r/pulls/1/reviews", "-f", "body=body", "-f", "event=APPROVE"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"review update", []string{"-XPUT", "repos/o/r/pulls/1/reviews/2", "-f", "body=body"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"review submit", []string{"repos/o/r/pulls/1/reviews/2/events", "-f", "body=body", "-f", "event=COMMENT"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"review dismiss", []string{"-XPUT", "repos/o/r/pulls/1/reviews/2/dismissals", "-f", "message=body"}, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"merge", []string{"-XPUT", "repos/o/r/pulls/1/merge", "-f", "commit_title=title", "-f", "commit_message="}, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Source: "inline"}}},
		{"release", []string{"repos/o/r/releases", "-f", "name=title", "-f", "body=notes"}, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Notes: &model.Text{Value: "notes", Source: "inline", Bytes: 5}}},
		{"release edit", []string{"-XPATCH", "repos/o/r/releases/1", "-f", "body="}, &model.Texts{Notes: &model.Text{Source: "inline"}}},
		{"raw at sign is inline", []string{"repos/o/r/issues", "-f", "body=@body.txt"}, &model.Texts{Body: &model.Text{Value: "@body.txt", Source: "inline", Bytes: 9}}},
		{"query ignored", []string{"-XPOST", "repos/o/r/issues?title=query&body=query"}, nil},
		{"GET fields ignored", []string{"-XGET", "repos/o/r/issues", "-f", "body=query"}, nil},
		{"nonstring omitted", []string{"repos/o/r/issues", "-F", "body=null", "-F", "title=123"}, nil},
		{"unrelated body omitted", []string{"repos/o/r/dispatches", "-f", "body=body"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := classify(t, knownInv(), tc.args...).Operations[0]
			if !reflect.DeepEqual(op.Texts, tc.want) {
				t.Fatalf("texts = %#v; want %#v", op.Texts, tc.want)
			}
		})
	}
}

func TestBodyTextProvenanceWithoutExtraReads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		stdin bool
		want  model.Text
	}{
		{"field file", []string{"repos/o/r/issues", "-F", "body=@body.txt"}, "body", false, model.Text{Value: "body", Source: "file", Path: "body.txt", Bytes: 4}},
		{"field stdin", []string{"repos/o/r/issues", "-F", "body=@-"}, "body", true, model.Text{Value: "body", Source: "stdin", Bytes: 4}},
		{"JSON file", []string{"repos/o/r/issues?body=query", "--input", "body.json", "-f", "body=also-query"}, `{"title":"","body":"body"}`, false, model.Text{Value: "body", Source: "file", Path: "body.json", Bytes: 4}},
		{"JSON stdin", []string{"repos/o/r/issues", "--input", "-"}, `{"body":"body"}`, true, model.Text{Value: "body", Source: "stdin", Bytes: 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			inv := knownInv()
			inv.ReadFile = func(path string) ([]byte, error) {
				reads++
				if path != tc.want.Path {
					t.Fatalf("unexpected path %q", path)
				}
				return []byte(tc.input), nil
			}
			if tc.stdin {
				inv.Stdin = model.StdinData{Status: model.StdinCaptured, Data: []byte(tc.input)}
			}
			op := classify(t, inv, tc.args...).Operations[0]
			if op.Texts == nil || op.Texts.Body == nil || *op.Texts.Body != tc.want {
				t.Fatalf("body text = %+v; want %+v", op.Texts, tc.want)
			}
			wantReads := 1
			if tc.stdin {
				wantReads = 0
			}
			if reads != wantReads {
				t.Fatalf("read count = %d; want %d", reads, wantReads)
			}
			if tc.name == "JSON file" && (op.Texts.Title == nil || op.Texts.Title.Value != "" || op.Parameters["body"] != "body") {
				t.Fatalf("empty title or canonical parameters lost: %+v", op)
			}
		})
	}
}

func TestUnavailableBodyText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		reason    string
		source    string
		path      string
		truncated bool
	}{
		{"file unavailable", "@missing.txt", "permission denied", "file", "missing.txt", false},
		{"file too large", "@large.txt", "file exceeds size limit", "file", "large.txt", true},
		{"stdin unavailable", "@-", "", "stdin", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			inv := knownInv()
			inv.ReadFile = func(string) ([]byte, error) {
				reads++
				return nil, errors.New(tc.reason)
			}
			op := classify(t, inv, "repos/o/r/issues", "-F", "body="+tc.value).Operations[0]
			want := model.Text{Source: tc.source, Path: tc.path, Truncated: tc.truncated}
			if op.Texts == nil || op.Texts.Body == nil || *op.Texts.Body != want || !unresolvedField(op, "texts.body", tc.source) {
				t.Fatalf("unavailable text metadata: %+v", op)
			}
			if (tc.source == "file" && reads != 1) || (tc.source == "stdin" && reads != 0) {
				t.Fatalf("unexpected reads: %d", reads)
			}
		})
	}
}

func TestUnknownJSONBodyDoesNotInventTexts(t *testing.T) {
	inv := knownInv()
	inv.ReadFile = func(string) ([]byte, error) { return nil, errors.New("unavailable") }
	op := classify(t, inv, "repos/o/r/issues?body=query", "--input", "missing.json").Operations[0]
	if op.Texts != nil {
		t.Fatalf("unknown JSON keys must not invent text: %+v", op.Texts)
	}
}
