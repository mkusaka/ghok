package transforms

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestApplyTextRoundTripProperties(t *testing.T) {
	query := `mutation($input:UpdateIssueInput!){updateIssue(input:$input){issue{id}}}`
	cases := []struct {
		name  string
		argv  []string
		files map[string]string
		stdin string
		field string
	}{
		{"native-inline", []string{"pr", "edit", "12", "--title=old", "-b", "neighbour"}, nil, "", "title"},
		{"native-bundled", []string{"pr", "merge", "12", "-dbold"}, nil, "", "body"},
		{"native-file", []string{"issue", "edit", "12", "--body-file", "original"}, map[string]string{"original": "old"}, "", "body"},
		{"native-stdin", []string{"issue", "edit", "12", "--body-file", "-"}, nil, "old", "body"},
		{"native-notes", []string{"release", "edit", "v1", "--notes", "old"}, nil, "", "notes"},
		{"rest-raw", []string{"api", "-X", "PATCH", "repos/o/r/issues/12", "-f", "title=old", "-f", "body=neighbour"}, nil, "", "title"},
		{"rest-typed", []string{"api", "-X", "PATCH", "repos/o/r/issues/12", "-Ftitle=old"}, nil, "", "title"},
		{"rest-file", []string{"api", "-X", "PATCH", "repos/o/r/issues/12", "--input", "original"}, map[string]string{"original": `{"title":"old","labels":["keep"],"body":"neighbour"}`}, "", "title"},
		{"rest-stdin", []string{"api", "-X", "PATCH", "repos/o/r/issues/12", "--input", "-"}, nil, `{"title":"old","body":"neighbour"}`, "title"},
		{"graphql-fields", []string{"api", "graphql", "-f", "query=" + query, "-f", "input[id]=I_1", "-f", "input[title]=old", "-f", "input[body]=neighbour"}, nil, "", "title"},
		{"graphql-input", []string{"api", "graphql", "--input", "original"}, map[string]string{"original": `{"query":` + quote(query) + `,"variables":{"input":{"id":"I_1","title":"old","body":"neighbour"}}}`}, "", "title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := model.Invocation{Argv: tc.argv, Env: map[string]string{"GH_REPO": "o/r"}}
			if tc.stdin != "" {
				inv.Stdin = model.StdinData{Status: model.StdinCaptured, Data: []byte(tc.stdin)}
			}
			inv.ReadFile = func(path string) ([]byte, error) {
				if value, ok := tc.files[path]; ok {
					return []byte(value), nil
				}
				return nil, os.ErrNotExist
			}
			before := normalize.Normalize(inv)
			path := "/operations/0/texts/" + tc.field + "/value"
			for _, value := range []string{"old", "changed", "", "true", "42", "@not-a-file", "unicode 日本語\nsecond line", "ghp_" + "not-a-real-secret"} {
				next, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{path: value}})
				if err != nil {
					t.Fatalf("set %q: %v", value, err)
				}
				if got := text(after.Operations[0].Texts, tc.field); got == nil || got.Value != value {
					t.Fatalf("text = %#v", got)
				}
				if value == "old" && !reflect.DeepEqual(next.Argv, inv.Argv) {
					t.Fatalf("self-set rewrote argv: %v", next.Argv)
				}
				for _, path := range next.TemporaryFiles {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode().Perm() != 0600 {
						t.Fatalf("temporary permissions %o", info.Mode().Perm())
					}
				}
				cleanup()
				cleanup()
				for _, path := range next.TemporaryFiles {
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("temporary survived cleanup: %v", err)
					}
				}
				if !reflect.DeepEqual(inv.Argv, tc.argv) {
					t.Fatal("original argv mutated")
				}
				if tc.stdin != "" && string(inv.Stdin.Data) != tc.stdin {
					t.Fatal("original stdin mutated")
				}
			}
		})
	}
}

func TestApplyJSONInsertUnsetAndParameters(t *testing.T) {
	inv := model.Invocation{Argv: []string{"api", "-X", "PATCH", "repos/o/r/issues/12", "--input", "-"}, Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte(`{"body":"keep","labels":["old"]}`)}}
	before := normalize.Normalize(inv)
	response := protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/title/value": "new", "/operations/0/parameters/labels": []any{"a", "b"}, "/operations/0/parameters/milestone": json.Number("4")}, Unset: []string{"/operations/0/texts/body/value"}}
	next, after, cleanup, err := Apply(inv, before, response)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if after.Operations[0].Texts.Body != nil || after.Operations[0].Texts.Title.Value != "new" {
		t.Fatalf("texts %#v", after.Operations[0].Texts)
	}
	if !equal(after.Operations[0].Parameters["labels"], []string{"a", "b"}) {
		t.Fatal("labels not changed")
	}
	if !strings.Contains(string(next.Stdin.Data), `"milestone":4`) {
		t.Fatalf("stdin %s", next.Stdin.Data)
	}
}

func TestApplyRejectsUnsupportedAtomically(t *testing.T) {
	cases := [][]string{
		{"pr", "create", "--fill"},
		{"pr", "create", "--editor", "--body", "old", "--title", "old"},
		{"api", "graphql", "-f", `query=mutation{updateIssue(input:{id:"I_1",body:"old"}){issue{id}}}`},
		{"api", "graphql", "-f", `query=mutation($body:String="old"){updateIssue(input:{id:"I_1",body:$body}){issue{id}}}`},
		{"api", "graphql", "-f", `query=mutation($body:String!){a:updateIssue(input:{id:"I_1",body:$body}){issue{id}} b:updateIssue(input:{id:"I_2",body:$body}){issue{id}}}`, "-f", "body=old"},
	}
	for _, argv := range cases {
		inv := model.Invocation{Argv: argv}
		before := normalize.Normalize(inv)
		next, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/body/value": "new"}})
		cleanup()
		if !errors.Is(err, ErrUnsupportedEdit) {
			t.Fatalf("%v: %v", argv, err)
		}
		if !reflect.DeepEqual(next, inv) || !reflect.DeepEqual(after, before) {
			t.Fatal("failed edit was not atomic")
		}
	}
}

func TestApplyRejectsClobberedNeighbour(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "edit", "12", "--title", "old", "--body", "keep"}}
	before := normalize.Normalize(inv)
	loc := before.Operations[0].Editable["texts/title/value"]
	loc.Spans = append(loc.Spans, model.ArgSpan{Start: 5, End: 7})
	before.Operations[0].Editable["texts/title/value"] = loc
	_, _, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/title/value": "new"}})
	cleanup()
	if !errors.Is(err, ErrUnsupportedEdit) {
		t.Fatalf("clobbered body accepted: %v", err)
	}
}

func TestApplyInvalidPointers(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "edit", "12", "--title", "old"}}
	before := normalize.Normalize(inv)
	for _, path := range []string{"operations/0/texts/title/value", "/operations/00/texts/title/value", "/operations/-1/texts/title/value", "/operations/1/texts/title/value", "/operations/0/texts/body", "/operations/0/texts/title/source", "/operations/0/parameters/x/y", "/operations/0/parameters/x~2y", "/operations/0/target/host"} {
		_, _, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{path: "new"}})
		cleanup()
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestApplyNoopKeepsAliasTransport(t *testing.T) {
	inv := model.Invocation{Argv: []string{"edit-it", "12", "--title", "old"}, Aliases: map[string]string{"edit-it": "pr edit"}}
	before := normalize.Normalize(inv)
	next, _, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/title/value": "old"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Argv, inv.Argv) {
		t.Fatal("self-set expanded execution alias")
	}
	next, _, cleanup, err = Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/title/value": "new"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Argv) < 2 || next.Argv[0] != "pr" || next.Argv[1] != "edit" {
		t.Fatalf("changed edit did not use expanded argv: %v", next.Argv)
	}
}

func quote(value string) string { data, _ := json.Marshal(value); return string(data) }

func TestProjectionClobberedNeighbour(t *testing.T) {
	a := model.Operation{Family: "issue.edit", Action: "issue.edit", Effect: model.EffectWrite, Execution: model.ExecutionWillRun, Parameters: map[string]any{"title": "old", "labels": []any{"keep"}}, Texts: &model.Texts{Title: &model.Text{Value: "old", Source: "inline", Bytes: 3}, Body: &model.Text{Value: "keep", Source: "inline", Bytes: 4}}}
	b := a
	b.Parameters = map[string]any{"title": "new", "labels": []any{"keep"}}
	b.Texts = &model.Texts{Title: &model.Text{Value: "new", Source: "file", Path: "temporary", Bytes: 3}, Body: a.Texts.Body}
	paths := []edit{{parts: []string{"texts", "title", "value"}, loc: model.EditLocator{Parameter: "title", Text: "title"}}}
	if !equal(projection(a, paths), projection(b, paths)) {
		t.Fatal("edited prose should be excluded from projection")
	}
	b.Parameters["labels"] = []any{"clobbered"}
	if equal(projection(a, paths), projection(b, paths)) {
		t.Fatal("unedited parameter was omitted from projection")
	}
	b.Parameters["labels"] = []any{"keep"}
	b.Texts.Body = &model.Text{Value: "clobbered", Source: "inline", Bytes: 9}
	if equal(projection(a, paths), projection(b, paths)) {
		t.Fatal("unedited body was omitted from projection")
	}
}

func TestApplyJSONPreservesLargeNumbersAndEscapedKeys(t *testing.T) {
	original := `{"title":"old","big":900719925474099312345,"decimal":1.234567890123456789,"a/b":null}`
	inv := model.Invocation{Argv: []string{"api", "-XPATCH", "repos/o/r/issues/1", "--input", "-"}, Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte(original)}}
	before := normalize.Normalize(inv)
	next, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/title/value": "new", "/operations/0/parameters/a~1b": "value"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(next.Stdin.Data), `900719925474099312345`) || !strings.Contains(string(next.Stdin.Data), `1.234567890123456789`) {
		t.Fatalf("number precision changed: %s", next.Stdin.Data)
	}
	if after.Operations[0].Parameters["a/b"] != "value" {
		t.Fatal("escaped pointer key not edited")
	}
	if string(inv.Stdin.Data) != original {
		t.Fatal("original input mutated")
	}
}

func TestApplyGraphQLRepeatedAndNestedVariables(t *testing.T) {
	query := `mutation($input:UpdateIssueInput!){updateIssue(input:$input){issue{id}}}`
	inv := model.Invocation{Argv: []string{"api", "graphql", "-f", "query=" + query, "-f", "input[id]=I_1", "-f", "input[body]=keep", "-f", "input[labelIds][]=L1", "-f", "input[metadata][name]=old", "-F", "input[metadata][enabled]=true"}}
	before := normalize.Normalize(inv)
	for _, value := range []any{[]any{"L2", "L3"}, []any{}, []any{"42", "true", "@not-a-file"}, []any{"true", true, "42", json.Number("42"), nil, "@not-a-file"}} {
		next, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/parameters/labelIds": value, "/operations/0/parameters/metadata": map[string]any{"name": "new", "enabled": false}}})
		cleanup()
		if err != nil {
			t.Fatal(err)
		}
		if !equal(after.Operations[0].Parameters["labelIds"], value) {
			t.Fatal("repeated fields did not round-trip")
		}
		if !equal(after.Operations[0].Parameters["metadata"], map[string]any{"name": "new", "enabled": false}) {
			t.Fatal("nested fields did not round-trip")
		}
		if reflect.DeepEqual(next.Argv, inv.Argv) {
			t.Fatal("changed fields retained original argv")
		}
	}
	_, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Unset: []string{"/operations/0/parameters/labelIds"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Operations[0].Parameters["labelIds"]; exists {
		t.Fatal("array field not removed")
	}
}

func TestApplyNativeTargetUnset(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "edit", "12", "--title", "old", "-R", "o/r"}}
	before := normalize.Normalize(inv)
	_, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Unset: []string{"/operations/0/target/number"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if after.Operations[0].Target.Number != nil {
		t.Fatal("number selector not removed")
	}
}

func TestApplyRejectsRESTNonTextFieldInsertion(t *testing.T) {
	inv := model.Invocation{Argv: []string{"api", "-XPATCH", "repos/o/r/issues/1", "-f", "body=old"}}
	before := normalize.Normalize(inv)
	_, _, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/parameters/labels": []any{"new"}}})
	defer cleanup()
	if !errors.Is(err, ErrUnsupportedEdit) {
		t.Fatalf("non-text field insertion accepted: %v", err)
	}
}

func TestApplyCleansFailedFileRewrite(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	inv := model.Invocation{Argv: []string{"issue", "edit", "12", "--body-file", "original", "--title", "keep"}, ReadFile: func(string) ([]byte, error) { return []byte("old"), nil }}
	before := normalize.Normalize(inv)
	loc := before.Operations[0].Editable["texts/body/value"]
	loc.Spans = append(loc.Spans, model.ArgSpan{Start: 5, End: 7})
	before.Operations[0].Editable["texts/body/value"] = loc
	next, _, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/body/value": "new"}})
	cleanup()
	if !errors.Is(err, ErrUnsupportedEdit) {
		t.Fatalf("clobbered title accepted: %v", err)
	}
	if !reflect.DeepEqual(next.Argv, inv.Argv) || len(next.TemporaryFiles) != 0 {
		t.Fatal("failed attempt leaked invocation changes")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed attempt left temporary files: %v", entries)
	}
}

func TestApplyNativeBundledConcurrentEdits(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "create", "-dbold", "-t", "title", "--label", "keep"}}
	before := normalize.Normalize(inv)
	_, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/body/value": "new", "/operations/0/parameters/draft": false}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if after.Operations[0].Texts.Body.Value != "new" || after.Operations[0].Parameters["draft"] != false {
		t.Fatalf("bundle edits did not round-trip: %+v", after.Operations[0])
	}
}

func TestApplyNativeTargetInsertion(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "edit", "--title", "old"}}
	before := normalize.Normalize(inv)
	_, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/target/number": json.Number("12"), "/operations/0/target/repository": "o/r"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	target := after.Operations[0].Target
	if target.Number == nil || *target.Number != 12 || target.Repository != "o/r" {
		t.Fatalf("target insertion failed: %+v", target)
	}
}

func TestApplyCanonicalMergeParameters(t *testing.T) {
	for _, tc := range []struct {
		argv  []string
		body  string
		path  string
		value string
	}{
		{[]string{"api", "-XPUT", "repos/o/r/pulls/12/merge", "--input", "-"}, `{"merge_method":"merge","commit_title":"keep"}`, "/operations/0/parameters/method", "squash"},
		{[]string{"api", "graphql", "--input", "-"}, `{"query":"mutation($input:MergePullRequestInput!){mergePullRequest(input:$input){pullRequest{id}}}","variables":{"input":{"pullRequestId":"P_1","mergeMethod":"MERGE","commitHeadline":"keep"}}}`, "/operations/0/parameters/mergeMethod", "SQUASH"},
	} {
		inv := model.Invocation{Argv: tc.argv, Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte(tc.body)}}
		before := normalize.Normalize(inv)
		_, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{tc.path: tc.value}})
		cleanup()
		if err != nil {
			t.Fatal(err)
		}
		if after.Operations[0].Parameters["method"] != "squash" {
			t.Fatalf("canonical method: %+v", after.Operations[0].Parameters)
		}
	}
}

func TestApplyNativeParameterRoundTripProperties(t *testing.T) {
	for _, tc := range []struct {
		argv     []string
		key      string
		original any
		values   []any
	}{
		{[]string{"pr", "create", "-t", "title", "-b", "body", "--label", "old", "--label", "old"}, "label", []string{"old", "old"}, []any{[]any{"a", "b"}, []any{"comma,label", "quoted\"label"}, []any{}}},
		{[]string{"pr", "create", "-t", "title", "-b", "body", "--assignee", "old"}, "assignee", []string{"old"}, []any{[]any{"new", "other"}}},
		{[]string{"pr", "create", "-t", "title", "-b", "body", "--reviewer", "old"}, "reviewer", []string{"old"}, []any{[]any{"new", "team/name"}}},
		{[]string{"pr", "create", "-t", "title", "-b", "body", "--base", "main"}, "base", "main", []any{"next", "release/日本語"}},
		{[]string{"pr", "create", "-t", "title", "-b", "body", "--draft"}, "draft", true, []any{false}},
		{[]string{"issue", "edit", "12", "--milestone", "old"}, "milestone", "old", []any{"new", ""}},
	} {
		inv := model.Invocation{Argv: tc.argv}
		before := normalize.Normalize(inv)
		values := append([]any{tc.original}, tc.values...)
		for _, value := range values {
			next, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/parameters/" + tc.key: value}})
			cleanup()
			if err != nil {
				t.Fatalf("%s=%#v: %v", tc.key, value, err)
			}
			if !equal(after.Operations[0].Parameters[tc.key], value) {
				t.Fatalf("%s did not round-trip", tc.key)
			}
			if equal(tc.original, value) && !reflect.DeepEqual(next.Argv, inv.Argv) {
				t.Fatalf("%s self-set changed original argv", tc.key)
			}
		}
	}
}

func TestApplyRejectsConflictingSelfSetAlias(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "edit", "12", "--title", "old"}}
	before := normalize.Normalize(inv)
	_, _, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/title/value": "new", "/operations/0/parameters/title": "old"}})
	defer cleanup()
	if !errors.Is(err, ErrUnsupportedEdit) {
		t.Fatalf("conflicting semantic aliases accepted: %v", err)
	}
}

func TestApplyNativeOptionalTextInsertUnset(t *testing.T) {
	for _, argv := range [][]string{{"pr", "review", "12", "--approve"}, {"pr", "close", "12"}, {"pr", "reopen", "12"}, {"issue", "close", "12"}, {"issue", "reopen", "12"}} {
		inv := model.Invocation{Argv: argv}
		before := normalize.Normalize(inv)
		next, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/body/value": "comment"}})
		cleanup()
		if err != nil {
			t.Fatalf("%v insert: %v", argv, err)
		}
		if text(after.Operations[0].Texts, "body").Value != "comment" {
			t.Fatalf("%v body not inserted", argv)
		}
		_, after, cleanup, err = Apply(next, after, protocol.TransformResponse{SchemaVersion: 1, Unset: []string{"/operations/0/texts/body/value"}})
		cleanup()
		if err != nil {
			t.Fatalf("%v unset: %v", argv, err)
		}
		if text(after.Operations[0].Texts, "body") != nil {
			t.Fatalf("%v body not removed", argv)
		}
	}
}

func TestApplyBundleFlagLookingValueIsNotRetained(t *testing.T) {
	inv := model.Invocation{Argv: []string{"pr", "create", "-db", "-d", "-t", "title"}}
	before := normalize.Normalize(inv)
	_, after, cleanup, err := Apply(inv, before, protocol.TransformResponse{SchemaVersion: 1, Set: map[string]any{"/operations/0/texts/body/value": "new"}, Unset: []string{"/operations/0/parameters/draft"}})
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := after.Operations[0].Parameters["draft"]; present {
		t.Fatal("body argument resembling a flag restored removed draft flag")
	}
	if after.Operations[0].Texts.Body.Value != "new" {
		t.Fatal("body edit was lost")
	}
}
