package graphql_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestMutationVariableLocators(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
		fields   []string
		path     []string
		want     bool
	}{
		{"scalar variable", `mutation($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`, []string{"-f", "body=hello"}, []string{"body"}, true},
		{"nested input", `mutation($input:AddCommentInput!){addComment(input:$input){clientMutationId}}`, []string{"-f", "input[subjectId]=I", "-f", "input[body]=hello"}, []string{"input", "body"}, true},
		{"file variable", `mutation($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`, []string{"-F", "body=@body.txt"}, []string{"body"}, true},
		{"literal", `mutation{addComment(input:{subjectId:"I",body:"hello"}){clientMutationId}}`, []string{"-f", "body=unused"}, nil, false},
		{"default", `mutation($body:String="hello"){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`, nil, nil, false},
		{"default overridden", `mutation($body:String="default"){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`, []string{"-f", "body=hello"}, []string{"body"}, true},
		{"object default", `mutation($input:AddCommentInput={subjectId:"I",body:"hello"}){addComment(input:$input){clientMutationId}}`, nil, nil, false},
		{"missing variable", `mutation($body:String){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := model.Invocation{ReadFile: func(string) ([]byte, error) { return []byte("hello"), nil }}
			args := append([]string{"graphql", "-f", "query=" + tc.document}, tc.fields...)
			op := onlyOperation(t, normalizeArgs(t, inv, args...))
			locator, found := op.Editable["texts/body/value"]
			if found != tc.want {
				t.Fatalf("body locator present = %t; want %t: %+v", found, tc.want, op.Editable)
			}
			if tc.want {
				want := model.EditLocator{Kind: "api-field", Key: tc.path, Parameter: "body", Text: "body"}
				if !reflect.DeepEqual(locator, want) || !reflect.DeepEqual(op.Editable["parameters/body"], want) {
					t.Fatalf("coupled locators = %+v; want %+v", op.Editable, want)
				}
			}
			assertLocatorJSONPrivate(t, op)
		})
	}
}

func TestMutationLocatorTransportRestrictions(t *testing.T) {
	document := `mutation($input:UpdateRepositoryInput!){updateRepository(input:$input){clientMutationId}}`
	op := onlyOperation(t, normalizeDocument(t, document, "-f", "input[repositoryId]=R", "-F", "input[hasIssuesEnabled]=true", "-F", "input[count]=12", "-f", "input[topics][]=one", "-f", "input[metadata][name]=x", "-F", "input[description]=null"))
	for _, key := range []string{"repositoryId", "hasIssuesEnabled", "count", "topics", "metadata"} {
		want := model.EditLocator{Kind: "api-field", Key: []string{"input", key}, Parameter: key}
		if !reflect.DeepEqual(op.Editable["parameters/"+key], want) {
			t.Fatalf("transport %s locator = %+v; want %+v", key, op.Editable, want)
		}
	}
	for _, key := range []string{"description", "*"} {
		if _, found := op.Editable["parameters/"+key]; found {
			t.Fatalf("unsupported API field %s has a locator: %+v", key, op.Editable)
		}
	}
	document = `mutation($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`
	op = onlyOperation(t, normalizeArgs(t, model.Invocation{}, "https://api.github.com/graphql", "-f", "query="+document, "-f", "variables[body]=hello"))
	if got := op.Editable["parameters/body"].Key; !reflect.DeepEqual(got, []string{"variables", "body"}) {
		t.Fatalf("ungrouped transport path = %v", got)
	}
	op = onlyOperation(t, normalizeDocument(t, document, "-X", "GET", "-f", "body=hello"))
	if len(op.Editable) != 0 {
		t.Fatalf("GET query fields must not become variable locators: %+v", op.Editable)
	}
}

func TestMutationContainerVariableLocators(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
	}{
		{"repeated array", []string{"-f", "value[]=one", "-f", "value[]=two"}},
		{"empty array", []string{"-f", "value[]"}},
		{"nested object", []string{"-f", "value[name]=hello", "-F", "value[enabled]=true"}},
		{"object array", []string{"-f", "value[][context]=ci", "-F", "value[][appId]=42"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := `mutation($value:CustomInput!){updateRepository(input:{repositoryId:"R",metadata:$value}){clientMutationId}}`
			op := onlyOperation(t, normalizeDocument(t, document, tc.fields...))
			want := model.EditLocator{Kind: "api-field", Key: []string{"value"}, Parameter: "metadata"}
			if !reflect.DeepEqual(op.Editable["parameters/metadata"], want) {
				t.Fatalf("container locator = %+v; want %+v", op.Editable, want)
			}
			if _, found := op.Editable["texts/body/value"]; found {
				t.Fatalf("container must not invent text semantics: %+v", op.Editable)
			}
			assertLocatorJSONPrivate(t, op)
			shared := `mutation($value:CustomInput!){a:updateRepository(input:{repositoryId:"R",metadata:$value}){clientMutationId} b:updateRepository(input:{repositoryId:"S",metadata:$value}){clientMutationId}}`
			for _, other := range normalizeDocument(t, shared, tc.fields...).Operations {
				if len(other.Editable) != 0 {
					t.Fatalf("shared container must not be editable: %+v", other.Editable)
				}
			}
		})
	}
}

func TestMutationJSONLocatorsAndInsertions(t *testing.T) {
	for _, path := range []string{"body.json", "-"} {
		t.Run(path, func(t *testing.T) {
			document := `mutation($input:CreateIssueInput!){createIssue(input:$input){clientMutationId}}`
			payload, err := json.Marshal(map[string]any{"query": document, "variables": map[string]any{"input": map[string]any{"repositoryId": "R", "labels": []string{"one"}}}})
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			inv := model.Invocation{
				ReadFile: func(string) ([]byte, error) { reads++; return payload, nil },
				Stdin:    model.StdinData{Status: model.StdinCaptured, Data: payload},
			}
			op := onlyOperation(t, normalizeArgs(t, inv, "graphql", "--input", path, "-f", "title=query-only"))
			for _, key := range []string{"title", "body", "labels", "*"} {
				want := model.EditLocator{Kind: "json", Input: path, Key: []string{"variables", "input", key}, Parameter: key}
				if key == "title" || key == "body" {
					want.Text = key
					if !reflect.DeepEqual(op.Editable["texts/"+key+"/value"], want) {
						t.Fatalf("missing JSON text insertion locator: %+v", op.Editable)
					}
				}
				if !reflect.DeepEqual(op.Editable["parameters/"+key], want) {
					t.Fatalf("JSON %s locator = %+v; want %+v", key, op.Editable, want)
				}
			}
			if op.Texts != nil || op.Parameters["title"] != nil || reads != map[bool]int{true: 0, false: 1}[path == "-"] {
				t.Fatalf("locators changed public values or reread input: op=%+v reads=%d", op, reads)
			}
			assertLocatorJSONPrivate(t, op)
		})
	}
}

func TestMutationJSONScalarAndLiteralSources(t *testing.T) {
	document := `mutation($headline:String!, $body:String="default"){mergePullRequest(input:{pullRequestId:"PR",commitHeadline:$headline,commitBody:$body}){clientMutationId}}`
	payload, err := json.Marshal(map[string]any{"query": document, "variables": map[string]any{"headline": "title"}})
	if err != nil {
		t.Fatal(err)
	}
	op := onlyOperation(t, normalizeArgs(t, model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: payload}}, "graphql", "--input", "-"))
	want := model.EditLocator{Kind: "json", Input: "-", Key: []string{"variables", "headline"}, Parameter: "commitHeadline", Text: "title"}
	if !reflect.DeepEqual(op.Editable["texts/title/value"], want) || !reflect.DeepEqual(op.Editable["parameters/commitHeadline"], want) {
		t.Fatalf("semantic headline locator = %+v; want %+v", op.Editable, want)
	}
	for _, key := range []string{"parameters/commitBody", "texts/body/value", "parameters/pullRequestId", "parameters/*"} {
		if _, found := op.Editable[key]; found {
			t.Fatalf("default/literal/insertion source must not be editable: %+v", op.Editable)
		}
	}
}

func TestMutationSharedVariableLocatorsRejected(t *testing.T) {
	for _, document := range []string{
		`mutation($body:String!){a:addComment(input:{subjectId:"I",body:$body}){clientMutationId} b:addComment(input:{subjectId:"J",body:$body}){clientMutationId}}`,
		`mutation($input:AddCommentInput!){a:addComment(input:$input){clientMutationId} b:addComment(input:$input){clientMutationId}}`,
		`mutation($body:String!){addComment(input:{subjectId:"I",body:$body},other:$body){clientMutationId}}`,
		`mutation($body:String!){addComment(input:{subjectId:"I",body:$body}) @custom(value:$body){clientMutationId}}`,
		`mutation($body:String!) @custom(value:$body){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`,
		`mutation($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId @custom(value:$body)}}`,
		`mutation($body:String!){... @custom(value:$body){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}}`,
		`mutation($body:String!){...Comment @custom(value:$body)} fragment Comment on Mutation {addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`,
		`mutation($body:String!){...Comment} fragment Comment on Mutation @custom(value:$body){addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`,
		`mutation($body:String!){...Comment ...Comment} fragment Comment on Mutation {addComment(input:{subjectId:"I",body:$body}){clientMutationId}}`,
		`mutation($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId} other:addComment(input:{subjectId:"J",body:"literal"},other:$body){clientMutationId}}`,
	} {
		t.Run(document, func(t *testing.T) {
			result := normalizeDocument(t, document, "-f", "body=hello", "-f", "input[subjectId]=I", "-f", "input[body]=hello")
			if len(result.Errors) != 0 {
				t.Fatalf("unexpected analysis errors: %v", result.Errors)
			}
			for _, op := range result.Operations {
				if len(op.Editable) != 0 {
					t.Fatalf("shared variable must not be editable: %+v", op.Editable)
				}
			}
		})
	}
}

func TestMutationUnselectedVariableUsesDoNotBlockLocators(t *testing.T) {
	document := `mutation Selected($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId}} mutation Other($body:String!){addComment(input:{subjectId:"J",body:$body}){clientMutationId}}`
	result := normalizeDocument(t, document, "-f", "operationName=Selected", "-f", "body=hello")
	if len(result.Operations) != 2 || result.Operations[0].Editable["parameters/body"].Kind != "api-field" || len(result.Operations[1].Editable) != 0 {
		t.Fatalf("selected-operation locator isolation: %+v", result.Operations)
	}
}

func TestMutationSharedJSONInputRejectsAllKeys(t *testing.T) {
	for _, neighbour := range []string{
		` b:addComment(input:$input){clientMutationId}`,
		` b:addComment(input:{subjectId:"J",body:"unchanged"}) @custom(value:$input){clientMutationId}`,
	} {
		document := `mutation($input:AddCommentInput!){a:addComment(input:$input){clientMutationId}` + neighbour + `}`
		payload, err := json.Marshal(map[string]any{"query": document, "variables": map[string]any{"input": map[string]any{"subjectId": "I", "body": "hello"}}})
		if err != nil {
			t.Fatal(err)
		}
		result := normalizeArgs(t, model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: payload}}, "graphql", "--input", "-")
		if len(result.Errors) != 0 {
			t.Fatalf("unexpected analysis errors: %v", result.Errors)
		}
		for _, op := range result.Operations {
			if len(op.Editable) != 0 {
				t.Fatalf("shared JSON input must reject every key and insertion: %+v", op.Editable)
			}
		}
	}
}

func assertLocatorJSONPrivate(t testing.TB, op model.Operation) {
	t.Helper()
	with, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	op.Editable = nil
	without, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(with, without) {
		t.Fatalf("private locators changed public JSON: %s != %s", with, without)
	}
}

func FuzzMutationVariableLocatorIsolation(f *testing.F) {
	f.Add("", false)
	f.Add("日本語\nbody", true)
	f.Add("literal @input [value]", false)
	f.Fuzz(func(t *testing.T, body string, shared bool) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		document := `mutation($body:String!){addComment(input:{subjectId:"I",body:$body}){clientMutationId}`
		if shared {
			document += ` other:addComment(input:{subjectId:"J",body:$body}){clientMutationId}`
		}
		document += `}`
		result := normalizeDocument(t, document, "-f", "body="+body)
		for _, op := range result.Operations {
			locator, found := op.Editable["texts/body/value"]
			if found == shared || (!shared && !reflect.DeepEqual(locator.Key, []string{"body"})) {
				t.Fatalf("variable-use isolation failed (shared=%t): %+v", shared, op.Editable)
			}
			if op.Texts == nil || op.Texts.Body == nil || op.Texts.Body.Value != body || op.Parameters["body"] != body {
				t.Fatalf("locator extraction changed source values: %+v", op)
			}
			assertLocatorJSONPrivate(t, op)
		}
	})
}
