package logging

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
)

func TestContentArgv(t *testing.T) {
	long := strings.Repeat("private", 12)
	repo := "owner/" + strings.Repeat("repository", 10)
	branch := "feature/" + strings.Repeat("branch", 15)
	url := "https://github.example/" + repo + "/pull/12"
	document := `mutation { addComment(input: {subjectId: "ISSUE_12", body: "ok"}) { clientMutationId } }`
	digest := sha256.Sum256([]byte(document))
	querySummary := fmt.Sprintf("[graphql document: %d bytes, sha256:%x]", len(document), digest[:8])
	cases := []struct {
		name string
		argv []string
		want []string
	}{
		{"short body", []string{"issue", "edit", "12", "--body", "ok"}, []string{"issue", "edit", "12", "--body", "[content: 2 bytes]"}},
		{"long body", []string{"pr", "create", "--body=" + long}, []string{"pr", "create", fmt.Sprintf("--body=[content: %d bytes]", len(long))}},
		{"short attached body", []string{"pr", "review", "12", "-bok"}, []string{"pr", "review", "12", "-b[content: 2 bytes]"}},
		{"short equals body", []string{"issue", "comment", "12", "-b=ok"}, []string{"issue", "comment", "12", "-b=[content: 2 bytes]"}},
		{"separate title", []string{"pr", "create", "-t", "ok"}, []string{"pr", "create", "-t", "[content: 2 bytes]"}},
		{"attached title", []string{"pr", "create", "-tok"}, []string{"pr", "create", "-t[content: 2 bytes]"}},
		{"long title", []string{"pr", "create", "--title=ok"}, []string{"pr", "create", "--title=[content: 2 bytes]"}},
		{"merge subject", []string{"pr", "merge", "12", "--subject", "ok", "-t=hi"}, []string{"pr", "merge", "12", "--subject", "[content: 2 bytes]", "-t=[content: 2 bytes]"}},
		{"message flags", []string{"custom", "write", "--message=ok", "-m", "hi"}, []string{"custom", "write", "--message=[content: 2 bytes]", "-m", "[content: 2 bytes]"}},
		{"attached message", []string{"custom", "write", "-m=ok"}, []string{"custom", "write", "-m=[content: 2 bytes]"}},
		{"notes and description", []string{"release", "create", "v1", "--notes=ok", "--description", "hi"}, []string{"release", "create", "v1", "--notes=[content: 2 bytes]", "--description", "[content: 2 bytes]"}},
		{"review event and positional prose", []string{"pr", "review", "12", "--comment", "ok"}, []string{"pr", "review", "12", "--comment", "[content: 2 bytes]"}},
		{"review after terminator", []string{"pr", "review", "--", "12", "ok", "--body"}, []string{"pr", "review", "--", "12", "[content: 2 bytes]", "[content: 6 bytes]"}},
		{"terminator stops flags", []string{"custom", "write", "--", "--body", "ok", "two words"}, []string{"custom", "write", "--", "--body", "ok", "[content: 9 bytes]"}},
		{"API field variants", []string{"api", "repos/o/r/issues", "-f", "body=ok", "-Finput[body]=hi", "-f=title=yo", "--field", "commitHeadline=go", "--raw-field=description=ha"}, []string{"api", "repos/o/r/issues", "-f", "body=[content: 2 bytes]", "-Finput[body]=[content: 2 bytes]", "-f=title=[content: 2 bytes]", "--field", "commitHeadline=[content: 2 bytes]", "--raw-field=description=[content: 2 bytes]"}},
		{"API bundle and nested paths", []string{"api", "graphql", "-isFinput.body=ok", "-finput[notes][]=hi", "--field=input[body][0]=yo"}, []string{"api", "graphql", "-isFinput.body=[content: 2 bytes]", "-finput[notes][]=[content: 2 bytes]", "--field=input[body][0]=[content: 2 bytes]"}},
		{"GraphQL original fingerprint", []string{"api", "graphql", "-fquery=" + document}, []string{"api", "graphql", "-fquery=" + querySummary}},
		{"invalid GraphQL fingerprint", []string{"api", "graphql", "--raw-field=query=oops"}, []string{"api", "graphql", "--raw-field=query=" + graphQLSummary("oops")}},
		{"unknown values", []string{"custom", "write", long, "two words", "--future=" + long, "-x" + long}, []string{"custom", "write", contentSummary(long), "[content: 9 bytes]", "--future=" + contentSummary(long), "-x" + contentSummary(long)}},
		{"fallback byte boundary", []string{"custom", "write", strings.Repeat("x", 64), strings.Repeat("x", 65), "a\u2003b"}, []string{"custom", "write", strings.Repeat("x", 64), "[content: 65 bytes]", "[content: 5 bytes]"}},
		{"structural flags", []string{"pr", "create", "-R", repo, "--base=" + branch, "-H" + branch, "--owner", long, "--hostname=" + long}, []string{"pr", "create", "-R", repo, "--base=" + branch, "-H" + branch, "--owner", long, "--hostname=" + long}},
		{"structural fields", []string{"api", "repos/" + repo, "-F", "input[headRefName]=" + branch, "--field=repository=" + repo, "-furl=" + url, "-fstate=OPEN", "-Fnumber=" + strings.Repeat("1", 70)}, []string{"api", "repos/" + repo, "-F", "input[headRefName]=" + branch, "--field=repository=" + repo, "-furl=" + url, "-fstate=OPEN", "-Fnumber=" + strings.Repeat("1", 70)}},
		{"structural positional branch", []string{"pr", "merge", branch, "-m"}, []string{"pr", "merge", branch, "-m"}},
		{"structural branch shorthand", []string{"pr", "checkout", "12", "-b" + branch}, []string{"pr", "checkout", "12", "-b" + branch}},
		{"structural repository URL", []string{"repo", "clone", url}, []string{"repo", "clone", url}},
		{"UTF8 byte count", []string{"pr", "create", "--title", "短文"}, []string{"pr", "create", "--title", "[content: 6 bytes]"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]string(nil), tc.argv...)
			got := redactRecord(redact.New(nil), Record{Argv: tc.argv}).Argv
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %#v, want %#v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.argv, before) {
				t.Fatal("original argv changed")
			}
		})
	}
}

func TestContentParametersSourceAndHookIsolation(t *testing.T) {
	type text string
	long := strings.Repeat("ordinary", 12)
	repo := "owner/" + long
	secret := "fake-private-" + "credential-value"
	document := `mutation { addComment(input: {subjectId: "ISSUE_12", body: "ok"}) { clientMutationId } } # ` + secret
	params := map[string]any{
		"body": "ok", "title": long, "message": "yo", "description": "ha", "subject": "hi", "commitHeadline": "go", "commitBody": "no", "comment": "ah", "notes": []string{"ok", long},
		"input":      map[string]any{"subjectId": "ISSUE_12", "body": "ok", "unknown": "two words", "opaque": long, "headRefName": long},
		"typed":      []map[string]text{{"title": "ok", "state": "OPEN"}},
		"typedArray": [2]text{"two words", "OPEN"},
		"raw":        json.RawMessage(`{"body":"ok","nested":{"description":"hi"},"count":1}`),
		"bytes":      []byte(`{"notes":"ok","state":"OPEN"}`), "opaqueBytes": []byte("private content"),
		"password":   []byte(`{"normal":"must-not-leak"}`),
		"repository": repo, "action": "pr.comment", "family": "pr.comment", "number": json.Number("123"), "enabled": true, "count": 12, "nil": nil,
		"query": document, "token": secret,
		"strings": map[string]string{"body": "ok", "ref": long},
	}
	source := &model.Source{Kind: "graphql", Argv: []string{"api", "graphql", "-f", "query=" + document, "-f", "input[body]=ok"}, GraphQL: document, Alias: "two words", Method: "POST", Path: "/repos/" + repo, Host: long}
	record := Record{Argv: source.Argv, Source: source, Operations: []model.Operation{{Family: "pr.comment", Action: "pr.comment", Target: model.Target{Repository: repo, Branch: long}, Parameters: params}}}
	before, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	r := redact.New(map[string]string{"GH_TOKEN": secret})
	logged := redactRecord(r, record)
	after, err := json.Marshal(record)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("record mutated: %v", err)
	}
	p := logged.Operations[0].Parameters
	for _, key := range []string{"body", "title", "message", "description", "subject", "commitHeadline", "commitBody", "comment"} {
		if got := p[key]; got != contentSummary(params[key].(string)) {
			t.Errorf("%s = %v", key, got)
		}
	}
	if got := p["strings"].(map[string]string); got["body"] != "[content: 2 bytes]" || got["ref"] != long {
		t.Errorf("typed string map = %#v", got)
	}
	if got := p["notes"]; !reflect.DeepEqual(got, []string{"[content: 2 bytes]", contentSummary(long)}) {
		t.Errorf("notes = %#v", got)
	}
	if got := p["input"].(map[string]any); got["body"] != "[content: 2 bytes]" || got["unknown"] != "[content: 9 bytes]" || got["opaque"] != contentSummary(long) || got["headRefName"] != long || got["subjectId"] != "ISSUE_12" {
		t.Errorf("input = %#v", got)
	}
	if got := p["typed"].([]any)[0].(map[string]any); got["title"] != "[content: 2 bytes]" || got["state"] != "OPEN" {
		t.Errorf("typed input = %#v", got)
	}
	if got := p["typedArray"]; !reflect.DeepEqual(got, []any{"[content: 9 bytes]", "OPEN"}) {
		t.Errorf("typed array = %#v", got)
	}
	for _, key := range []string{"raw", "bytes"} {
		var body []byte
		if key == "raw" {
			body = p[key].(json.RawMessage)
		} else {
			body = p[key].([]byte)
		}
		if bytes.Contains(body, []byte(`"ok"`)) || bytes.Contains(body, []byte(`"hi"`)) || !bytes.Contains(body, []byte("[content: 2 bytes]")) {
			t.Errorf("%s = %s", key, body)
		}
	}
	if p["password"] != "[REDACTED]" || p["token"] != "[REDACTED]" || p["number"] != json.Number("123") || p["count"] != 12 || p["enabled"] != true || p["nil"] != nil {
		t.Errorf("credentials or scalar values = %#v", p)
	}
	digest := sha256.Sum256([]byte(document))
	wantQuery := fmt.Sprintf("[graphql document: %d bytes, sha256:%x]", len(document), digest[:8])
	if logged.Source.GraphQL != wantQuery || p["query"] != wantQuery || logged.Source.Argv[3] != "query="+wantQuery {
		t.Errorf("original document fingerprint missing: source=%+v, query=%v", logged.Source, p["query"])
	}
	if logged.Source.Alias != "[content: 9 bytes]" || logged.Source.Path != source.Path || logged.Source.Host != source.Host || p["repository"] != repo || logged.Operations[0].Target.Repository != repo || logged.Operations[0].Target.Branch != long || logged.Operations[0].Action != "pr.comment" {
		t.Fatal("structural metadata changed or source prose was exposed")
	}
	hook := r.Input(protocol.Input{Operations: record.Operations})
	if hook.Operations[0].Parameters["body"] != "ok" || hook.Operations[0].Parameters["title"] != long {
		t.Fatal("log minimization affected hook prose")
	}
	// The returned record must not retain aliases into the original containers.
	logged.Source.Argv[0] = "changed"
	logged.Operations[0].Parameters["input"].(map[string]any)["body"] = "changed"
	after, _ = json.Marshal(record)
	if !bytes.Equal(before, after) {
		t.Fatal("sanitized record aliases the original")
	}
}

func TestTransformedRequestContentIsolation(t *testing.T) {
	secret := "transform-private-" + "credential"
	request := &Transformed{
		Argv:       []string{"issue", "comment", "12", "--body", "ordinary prose", "-R", secret},
		Transforms: []string{"attribute"}, Texts: map[string]int64{"body": 14},
	}
	record := Record{Argv: []string{"issue", "comment", "12", "--body", "original"}, Transformed: request}
	got := redactRecord(redact.New(map[string]string{"GH_TOKEN": secret}), record)
	if got.Argv[4] != "[content: 8 bytes]" || got.Transformed.Argv[4] != "[content: 14 bytes]" || got.Transformed.Argv[6] != "[REDACTED]" {
		t.Fatalf("original/transformed content exposed: %+v", got)
	}
	if request.Argv[4] != "ordinary prose" || request.Argv[6] != secret {
		t.Fatal("redaction mutated the executable request")
	}
	got.Transformed.Argv[0] = "changed"
	if request.Argv[0] != "issue" {
		t.Fatal("log aliases the executable request")
	}
}

func FuzzContentMinimization(f *testing.F) {
	f.Add("ok", "two words", "query { viewer { login } }")
	f.Add("短文", strings.Repeat("x", 65), `mutation { addComment(input: {body: "private", subjectId: "ID"}) { id } }`)
	f.Add("", "OPEN", "not graphql")
	f.Fuzz(func(t *testing.T, prose, unknown, document string) {
		params := map[string]any{"input": map[string]any{"body": prose, "unknown": unknown}, "title": []string{prose}, "query": document}
		argv := []string{"api", "graphql", "-Finput[body]=" + prose, "-fquery=" + document}
		record := Record{Argv: argv, Source: &model.Source{GraphQL: document, Argv: argv}, Operations: []model.Operation{{Parameters: params}}}
		before, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		got := redactRecord(redact.New(nil), record)
		wantProse := contentSummary(prose)
		input := got.Operations[0].Parameters["input"].(map[string]any)
		if input["body"] != wantProse || got.Argv[2] != "-Finput[body]="+wantProse || got.Operations[0].Parameters["title"].([]string)[0] != wantProse {
			t.Fatal("known prose was not withheld with original byte count")
		}
		if minimized := minimizeString(unknown, "unknown"); minimized != unknown && input["unknown"] != minimized {
			t.Fatal("generic content was not withheld")
		}
		if got.Argv[3] != "-fquery="+graphQLSummary(document) || got.Operations[0].Parameters["query"] != graphQLSummary(document) {
			t.Fatal("GraphQL argument fingerprint changed")
		}
		if document != "" && got.Source.GraphQL != graphQLSummary(document) {
			t.Fatal("GraphQL source fingerprint changed")
		}
		after, err := json.Marshal(record)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("input mutated")
		}
	})
}
