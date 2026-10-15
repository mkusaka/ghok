package graphql_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestMutationTexts(t *testing.T) {
	for _, tc := range []struct {
		field string
		input string
		want  *model.Texts
	}{
		{"createPullRequest", `repositoryId:"R", baseRefName:"main", headRefName:"topic", title:"title", body:"body"`, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"updatePullRequest", `pullRequestId:"PR", title:""`, &model.Texts{Title: &model.Text{Source: "inline"}}},
		{"createIssue", `repositoryId:"R", title:"title", body:"日本語"`, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Value: "日本語", Source: "inline", Bytes: 9}}},
		{"updateIssue", `id:"I", body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"addComment", `subjectId:"I", body:""`, &model.Texts{Body: &model.Text{Source: "inline"}}},
		{"updateIssueComment", `id:"C", body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"addPullRequestReview", `pullRequestId:"PR", event:COMMENT, body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"submitPullRequestReview", `pullRequestReviewId:"RV", event:COMMENT, body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"dismissPullRequestReview", `pullRequestReviewId:"RV", message:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"updatePullRequestReview", `pullRequestReviewId:"RV", body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"addPullRequestReviewComment", `pullRequestReviewId:"RV", body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"updatePullRequestReviewComment", `pullRequestReviewCommentId:"C", body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"addPullRequestReviewThread", `pullRequestId:"PR", body:"body"`, &model.Texts{Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"mergePullRequest", `pullRequestId:"PR", commitHeadline:"title", commitBody:""`, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Source: "inline"}}},
		{"enablePullRequestAutoMerge", `pullRequestId:"PR", commitHeadline:"title", commitBody:"body"`, &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Value: "body", Source: "inline", Bytes: 4}}},
		{"addComment", `subjectId:"I"`, nil},
		{"addComment", `subjectId:"I", body:null`, nil},
		{"createRepository", `name:"r", description:"not a body"`, nil},
	} {
		t.Run(tc.field+tc.input, func(t *testing.T) {
			document := fmt.Sprintf("mutation { %s(input: {%s}) { clientMutationId } }", tc.field, tc.input)
			op := onlyOperation(t, normalizeDocument(t, document))
			if !reflect.DeepEqual(op.Texts, tc.want) {
				t.Fatalf("texts = %#v; want %#v", op.Texts, tc.want)
			}
		})
	}
}

func TestMutationTextVariables(t *testing.T) {
	document := `mutation($body:String!, $headline:String!) { mergePullRequest(input:{pullRequestId:"PR", commitHeadline:$headline, commitBody:$body}) { clientMutationId } }`
	op := onlyOperation(t, normalizeDocument(t, document, "-f", "body=", "-f", "headline=title"))
	want := &model.Texts{Title: &model.Text{Value: "title", Source: "inline", Bytes: 5}, Body: &model.Text{Source: "inline"}}
	if !reflect.DeepEqual(op.Texts, want) || op.Parameters["commitBody"] != "" || op.Parameters["commitHeadline"] != "title" {
		t.Fatalf("resolved merge text or parameters lost: %+v", op)
	}
	document = `mutation($input:AddCommentInput!) { addComment(input:$input) { clientMutationId } }`
	op = onlyOperation(t, normalizeDocument(t, document, "-f", "input[subjectId]=I", "-f", "input[body]=body"))
	if op.Texts == nil || op.Texts.Body == nil || *op.Texts.Body != (model.Text{Value: "body", Source: "inline", Bytes: 4}) {
		t.Fatalf("input object variable: %+v", op)
	}
	document = `mutation($body:String!="default") { addComment(input:{subjectId:"I", body:$body}) { clientMutationId } }`
	op = onlyOperation(t, normalizeDocument(t, document))
	if op.Texts == nil || op.Texts.Body == nil || op.Texts.Body.Value != "default" || op.Texts.Body.Source != "inline" {
		t.Fatalf("default variable text: %+v", op)
	}
	document = `mutation($body:String!) { addComment(input:{subjectId:"I", body:$body}) { clientMutationId } }`
	op = onlyOperation(t, normalizeDocument(t, document))
	if op.Texts != nil {
		t.Fatalf("missing variable must not invent text: %+v", op.Texts)
	}
}

func TestMutationTextProvenanceWithoutExtraReads(t *testing.T) {
	literal := `mutation { addComment(input:{subjectId:"I", body:"body"}) { clientMutationId } }`
	variable := `mutation($body:String!) { addComment(input:{subjectId:"I", body:$body}) { clientMutationId } }`
	object := `mutation($input:AddCommentInput!) { addComment(input:$input) { clientMutationId } }`
	payload, err := json.Marshal(map[string]any{"query": variable, "variables": map[string]any{"body": "body"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		data  string
		stdin bool
		want  model.Text
	}{
		{"variable file", []string{"graphql", "-f", "query=" + variable, "-F", "body=@body.txt"}, "body", false, model.Text{Value: "body", Source: "file", Path: "body.txt", Bytes: 4}},
		{"nested variable file", []string{"graphql", "-f", "query=" + object, "-f", "input[subjectId]=I", "-F", "input[body]=@body.txt"}, "body", false, model.Text{Value: "body", Source: "file", Path: "body.txt", Bytes: 4}},
		{"variable stdin", []string{"graphql", "-f", "query=" + variable, "-F", "body=@-"}, "body", true, model.Text{Value: "body", Source: "stdin", Bytes: 4}},
		{"query file", []string{"graphql", "-F", "query=@query.graphql"}, literal, false, model.Text{Value: "body", Source: "file", Path: "query.graphql", Bytes: 4}},
		{"query stdin", []string{"graphql", "-F", "query=@-"}, literal, true, model.Text{Value: "body", Source: "stdin", Bytes: 4}},
		{"JSON file", []string{"graphql", "--input", "body.json", "-f", "body=query-only"}, string(payload), false, model.Text{Value: "body", Source: "file", Path: "body.json", Bytes: 4}},
		{"JSON stdin", []string{"graphql", "--input", "-"}, string(payload), true, model.Text{Value: "body", Source: "stdin", Bytes: 4}},
		{"inline variable with query file", []string{"graphql", "-F", "query=@query.graphql", "-f", "body=body"}, variable, false, model.Text{Value: "body", Source: "inline", Bytes: 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			inv := model.Invocation{ReadFile: func(string) ([]byte, error) {
				reads++
				return []byte(tc.data), nil
			}}
			if tc.stdin {
				inv.Stdin = model.StdinData{Status: model.StdinCaptured, Data: []byte(tc.data)}
			}
			op := onlyOperation(t, normalizeArgs(t, inv, tc.args...))
			if op.Texts == nil || op.Texts.Body == nil || *op.Texts.Body != tc.want {
				t.Fatalf("text = %+v; want %+v", op.Texts, tc.want)
			}
			wantReads := 1
			if tc.stdin {
				wantReads = 0
			}
			if reads != wantReads {
				t.Fatalf("read count = %d; want %d", reads, wantReads)
			}
		})
	}
}

func TestUnavailableMutationText(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, source := range []string{"file", "stdin"} {
			t.Run(fmt.Sprintf("nested=%t/%s", nested, source), func(t *testing.T) {
				document := `mutation($body:String!) { addComment(input:{subjectId:"I", body:$body}) { clientMutationId } }`
				key := "body"
				if nested {
					document = `mutation($input:AddCommentInput!) { addComment(input:$input) { clientMutationId } }`
					key = "input[body]"
				}
				path, wantPath := "large.txt", "large.txt"
				if source == "stdin" {
					path, wantPath = "-", ""
				}
				reads := 0
				inv := model.Invocation{ReadFile: func(string) ([]byte, error) {
					reads++
					return nil, errors.New("file exceeds size limit")
				}}
				op := onlyOperation(t, normalizeArgs(t, inv, "graphql", "-f", "query="+document, "-F", key+"=@"+path))
				want := model.Text{Source: source, Path: wantPath, Truncated: source == "file"}
				if op.Texts == nil || op.Texts.Body == nil || *op.Texts.Body != want || !hasUnresolved(op, "texts.body", source) {
					t.Fatalf("unavailable text metadata: %+v", op)
				}
				if (source == "file" && reads != 1) || (source == "stdin" && reads != 0) {
					t.Fatalf("unexpected reads: %d", reads)
				}
			})
		}
	}
}
