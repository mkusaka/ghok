package graphql_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
	"github.com/mkusaka/ghok/internal/normalize/graphql"
)

const mergeDocument = `mutation Merge($id: ID!) { mergePullRequest(input: {pullRequestId: $id, mergeMethod: SQUASH}) { clientMutationId } }`

func normalizeArgs(t testing.TB, inv model.Invocation, args ...string) model.Result {
	t.Helper()
	req, err := apiargs.Parse(args)
	if err != nil {
		t.Fatalf("parse API arguments: %v", err)
	}
	inv.Argv = append([]string{"api"}, args...)
	result := graphql.Normalize(inv, req)
	if result.SchemaVersion != model.SchemaVersion || result.Classification.Level != model.LevelL3 {
		t.Fatalf("unexpected result contract: version=%d classification=%+v", result.SchemaVersion, result.Classification)
	}
	return result
}

func normalizeDocument(t testing.TB, document string, extra ...string) model.Result {
	t.Helper()
	args := append([]string{"graphql", "-f", "query=" + document}, extra...)
	return normalizeArgs(t, model.Invocation{}, args...)
}

func onlyOperation(t testing.TB, result model.Result) model.Operation {
	t.Helper()
	if len(result.Operations) != 1 {
		t.Fatalf("want one operation, got %+v", result.Operations)
	}
	return result.Operations[0]
}

func requireUnknown(t testing.TB, result model.Result, reason model.UnknownReason) {
	t.Helper()
	if result.Classification.MutationFree {
		t.Fatalf("unknown request was marked mutation-free: %+v", result)
	}
	for _, op := range result.Operations {
		if op.Effect == model.EffectUnknown && op.UnknownReason != model.UnknownNone {
			if reason != model.UnknownNone && op.UnknownReason != reason {
				t.Fatalf("want unknown reason %q, got %+v", reason, op)
			}
			return
		}
	}
	t.Fatalf("want an explicit unknown-effect operation, got %+v", result.Operations)
}

func hasUnresolved(op model.Operation, field, source string) bool {
	for _, unresolved := range op.Unresolved {
		if unresolved.Field == field && unresolved.Source == source {
			return true
		}
	}
	return false
}

func TestNormalizeGraphQLCanonicalMerge(t *testing.T) {
	result := normalizeDocument(t, mergeDocument, "-f", "id=PR_node")
	op := onlyOperation(t, result)
	if op.Family != "pr.merge" || op.Action != "pr.merge.direct" || op.Parameters["method"] != "squash" {
		t.Fatalf("canonical merge relation lost: %+v", op)
	}
	if op.Target.NodeID != "PR_node" || op.Effect != model.EffectWrite || op.Execution != model.ExecutionWillRun {
		t.Fatalf("merge target/effect/execution: %+v", op)
	}
	if op.Origin.Field != "mergePullRequest" || op.Origin.Operation != "Merge" || op.Origin.OpType != "mutation" {
		t.Fatalf("merge origin: %+v", op.Origin)
	}
	if result.Classification.MutationFree || result.Classification.Confidence != model.ConfidenceExact || result.Classification.Resolution != model.ResolutionDeferred {
		t.Fatalf("merge classification: %+v", result.Classification)
	}
	if !hasUnresolved(op, "target.repository", "api") {
		t.Fatalf("node ID must defer repository lookup: %+v", op.Unresolved)
	}
	if result.Source.Kind != "graphql" || result.Source.GraphQL != mergeDocument {
		t.Fatalf("GraphQL source: %+v", result.Source)
	}
}

func TestNormalizeGraphQLRequestSources(t *testing.T) {
	body, err := json.Marshal(map[string]any{"query": mergeDocument, "variables": map[string]any{"id": "PR_node"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		inv  model.Invocation
	}{
		{"inline", []string{"graphql", "-f", "query=" + mergeDocument, "-f", "id=PR_node"}, model.Invocation{}},
		{"query file", []string{"graphql", "-F", "query=@query.graphql", "-f", "id=PR_node"}, model.Invocation{ReadFile: func(path string) ([]byte, error) {
			if path != "query.graphql" {
				return nil, fmt.Errorf("unexpected file %q", path)
			}
			return []byte(mergeDocument), nil
		}}},
		{"query stdin", []string{"graphql", "-F", "query=@-", "-f", "id=PR_node"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte(mergeDocument)}}},
		{"JSON file", []string{"graphql", "--input", "body.json"}, model.Invocation{ReadFile: func(path string) ([]byte, error) {
			if path != "body.json" {
				return nil, fmt.Errorf("unexpected file %q", path)
			}
			return body, nil
		}}},
		{"JSON stdin", []string{"graphql", "--input", "-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: body}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeArgs(t, tc.inv, tc.args...)
			op := onlyOperation(t, result)
			if op.Action != "pr.merge.direct" || op.Target.NodeID != "PR_node" || op.Parameters["method"] != "squash" || result.Classification.MutationFree {
				t.Fatalf("wrong reconstructed request: %+v", result)
			}
		})
	}
}

func TestNormalizeGraphQLInputFieldsAreURLParameters(t *testing.T) {
	body := `{"query":"mutation Merge($id: ID!) { mergePullRequest(input: {pullRequestId: $id}) { clientMutationId } }","variables":{"id":"body-node"},"operationName":"Merge"}`
	inv := model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte(body)}}
	result := normalizeArgs(t, inv, "graphql", "--input", "-", "-f", "query=query { viewer { login } }", "-f", "id=URL-node", "-f", "operationName=Wrong")
	op := onlyOperation(t, result)
	if op.Action != "pr.merge.direct" || op.Target.NodeID != "body-node" || op.Origin.Operation != "Merge" || result.Classification.MutationFree {
		t.Fatalf("URL parameters must not replace JSON body fields: %+v", result)
	}

	body = `{"query":"mutation($id: ID!) { mergePullRequest(input: {pullRequestId: $id}) { clientMutationId } }"}`
	inv.Stdin.Data = []byte(body)
	result = normalizeArgs(t, inv, "graphql", "--input", "-", "-f", "id=URL-node")
	op = onlyOperation(t, result)
	if op.Target.NodeID != "" || !hasUnresolved(op, "variables.id", "variable") {
		t.Fatalf("URL fields incorrectly supplied body variables: %+v", op)
	}
}

func TestNormalizeGraphQLReadDocuments(t *testing.T) {
	for name, document := range map[string]string{
		"anonymous":             `{ viewer { login } }`,
		"named":                 `query Viewer { viewer { login } }`,
		"introspection schema":  `{ __schema { queryType { name } } }`,
		"introspection type":    `query { __type(name: "Mutation") { name } }`,
		"string":                `query { search(query: "mutation { mergePullRequest }", type: ISSUE) { issueCount } }`,
		"comment":               "# mutation { mergePullRequest }\nquery { viewer { login } }",
		"field":                 `query { mutation }`,
		"argument":              `query { search(query: "mutation", type: ISSUE) { issueCount } }`,
		"operation named merge": `query mergePullRequest { viewer { login } }`,
		"nested merge field":    `query { viewer { mergePullRequest { id } } }`,
	} {
		t.Run(name, func(t *testing.T) {
			result := normalizeDocument(t, document)
			op := onlyOperation(t, result)
			if !result.Classification.MutationFree || result.Classification.Confidence != model.ConfidenceExact || op.Family != "graphql.query" || op.Action != "graphql.query" || op.Effect != model.EffectRead {
				t.Fatalf("parsed query must be read-only regardless of words: %+v", result)
			}
		})
	}
}

func TestNormalizeGraphQLAliasesAndMutationOrder(t *testing.T) {
	document := `mutation Many {
		first: mergePullRequest(input: {pullRequestId: "PR_1", mergeMethod: MERGE}) { clientMutationId }
		mergePullRequest: addStar(input: {starrableId: "REPO_1"}) { clientMutationId }
		last: mergePullRequest(input: {pullRequestId: "PR_2", mergeMethod: REBASE}) { clientMutationId }
	}`
	result := normalizeDocument(t, document)
	if len(result.Operations) != 3 {
		t.Fatalf("want all root mutations: %+v", result)
	}
	for i, want := range []struct{ field, alias, node string }{
		{"mergePullRequest", "first", "PR_1"},
		{"addStar", "mergePullRequest", "REPO_1"},
		{"mergePullRequest", "last", "PR_2"},
	} {
		op := result.Operations[i]
		if op.Origin.Field != want.field || op.Origin.Alias != want.alias || op.Target.NodeID != want.node || op.Effect != model.EffectWrite {
			t.Errorf("operation %d must follow AST field order and identity: %+v", i, op)
		}
	}
	if result.Operations[1].Family == "pr.merge" || result.Operations[1].Action == "pr.merge.direct" {
		t.Fatalf("alias was mistaken for schema field: %+v", result.Operations[1])
	}
}

func TestNormalizeGraphQLFragments(t *testing.T) {
	for name, document := range map[string]string{
		"spread":                `mutation { ...Merge } fragment Merge on Mutation { mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } }`,
		"nested spread":         `mutation { ...Outer } fragment Outer on Mutation { ...Inner } fragment Inner on Mutation { mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } }`,
		"inline typed":          `mutation { ... on Mutation { mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } } }`,
		"inline directive only": `mutation { ... @include(if: true) { mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } } }`,
	} {
		t.Run(name, func(t *testing.T) {
			result := normalizeDocument(t, document)
			op := onlyOperation(t, result)
			if op.Action != "pr.merge.direct" || op.Target.NodeID != "PR_1" || op.Execution != model.ExecutionWillRun || result.Classification.MutationFree {
				t.Fatalf("root fragment mutation not expanded: %+v", result)
			}
		})
	}
}

func TestNormalizeGraphQLRepeatedFragmentIsNotACycle(t *testing.T) {
	document := `mutation { ...Shared ...Shared } fragment Shared on Mutation { mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } }`
	result := normalizeDocument(t, document)
	if len(result.Operations) == 0 || result.Classification.MutationFree {
		t.Fatalf("repeated acyclic fragment lost its writes: %+v", result)
	}
	for _, op := range result.Operations {
		if op.Action != "pr.merge.direct" || op.Effect != model.EffectWrite {
			t.Fatalf("repeated fragment incorrectly classified as cycle: %+v", op)
		}
	}
}

func TestNormalizeGraphQLInvalidFragmentGraphs(t *testing.T) {
	for name, document := range map[string]string{
		"self cycle":         `mutation { ...Loop } fragment Loop on Mutation { ...Loop }`,
		"indirect cycle":     `mutation { ...A } fragment A on Mutation { ...B } fragment B on Mutation { ...A }`,
		"missing":            `mutation { ...Missing }`,
		"query missing":      `query { ...Missing }`,
		"query cycle":        `query { ...Loop } fragment Loop on Query { ...Loop }`,
		"write then missing": `mutation { mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } ...Missing }`,
	} {
		t.Run(name, func(t *testing.T) {
			requireUnknown(t, normalizeDocument(t, document), model.UnknownNone)
		})
	}
}

func TestNormalizeGraphQLDirectives(t *testing.T) {
	cases := []struct {
		name      string
		directive string
		fields    []string
		execution model.Execution
		free      bool
	}{
		{"skip true", `@skip(if: true)`, nil, model.ExecutionSkipped, true},
		{"skip false", `@skip(if: false)`, nil, model.ExecutionWillRun, false},
		{"include false", `@include(if: false)`, nil, model.ExecutionSkipped, true},
		{"include true", `@include(if: true)`, nil, model.ExecutionWillRun, false},
		{"variable skip", `@skip(if: $flag)`, []string{"-F", "flag=true"}, model.ExecutionSkipped, true},
		{"variable include", `@include(if: $flag)`, []string{"-F", "flag=false"}, model.ExecutionSkipped, true},
		{"variable run", `@include(if: $flag)`, []string{"-F", "flag=true"}, model.ExecutionWillRun, false},
		{"unresolved skip", `@skip(if: $flag)`, nil, model.ExecutionMaybe, false},
		{"unresolved include", `@include(if: $flag)`, nil, model.ExecutionMaybe, false},
		{"raw string is not boolean", `@skip(if: $flag)`, []string{"-f", "flag=true"}, model.ExecutionMaybe, false},
		{"proven skip dominates unresolved", `@skip(if: true) @include(if: $flag)`, nil, model.ExecutionSkipped, true},
	}
	for _, tc := range cases {
		for _, location := range []string{"field", "spread", "inline"} {
			t.Run(tc.name+"/"+location, func(t *testing.T) {
				field := `x: mergePullRequest(input: {pullRequestId: "PR_1"})`
				var document string
				switch location {
				case "field":
					document = `mutation($flag: Boolean) { ` + field + ` ` + tc.directive + ` { clientMutationId } }`
				case "spread":
					document = `mutation($flag: Boolean) { ...Merge ` + tc.directive + ` } fragment Merge on Mutation { ` + field + ` { clientMutationId } }`
				case "inline":
					document = `mutation($flag: Boolean) { ... on Mutation ` + tc.directive + ` { ` + field + ` { clientMutationId } } }`
				}
				result := normalizeDocument(t, document, tc.fields...)
				op := onlyOperation(t, result)
				if op.Execution != tc.execution || result.Classification.MutationFree != tc.free {
					t.Fatalf("directive proof incorrect: %+v", result)
				}
				if op.Execution != model.ExecutionWillRun && op.Evidence == "" {
					t.Fatalf("non-running/maybe execution needs evidence: %+v", op)
				}
			})
		}
	}
}

func TestNormalizeGraphQLDirectiveDefaultsAndUnknownDirectives(t *testing.T) {
	result := normalizeDocument(t, `mutation($flag: Boolean = true) { mergePullRequest(input: {pullRequestId: "PR_1"}) @skip(if: $flag) { clientMutationId } }`)
	if onlyOperation(t, result).Execution != model.ExecutionSkipped || !result.Classification.MutationFree {
		t.Fatalf("directive variable default not resolved: %+v", result)
	}
	result = normalizeDocument(t, `mutation { mergePullRequest(input: {pullRequestId: "PR_1"}) @future(if: false) { clientMutationId } }`)
	op := onlyOperation(t, result)
	if op.Execution != model.ExecutionWillRun || result.Classification.MutationFree || !strings.Contains(op.Detail, "future") {
		t.Fatalf("unknown directive must be noted, not interpreted: %+v", result)
	}
}

func TestNormalizeGraphQLSkippedUnknownMutation(t *testing.T) {
	result := normalizeDocument(t, `mutation { futureMutation(input: {}) @skip(if: true) { clientMutationId } }`)
	op := onlyOperation(t, result)
	if op.UnknownReason != model.UnknownMutation || op.Effect != model.EffectWrite || op.Execution != model.ExecutionSkipped || op.Evidence == "" || !result.Classification.MutationFree {
		t.Fatalf("proven skipped unknown write is mutation-free with evidence: %+v", result)
	}
}

func TestNormalizeGraphQLMixedExecutionIsNotMutationFree(t *testing.T) {
	result := normalizeDocument(t, `mutation { skipped: mergePullRequest(input: {pullRequestId: "PR_1"}) @skip(if: true) { clientMutationId } running: mergePullRequest(input: {pullRequestId: "PR_2"}) { clientMutationId } }`)
	if len(result.Operations) != 2 || result.Operations[0].Execution != model.ExecutionSkipped || result.Operations[1].Execution != model.ExecutionWillRun || result.Classification.MutationFree {
		t.Fatalf("one skipped field must not hide another write: %+v", result)
	}
}

func TestNormalizeGraphQLOperationSelection(t *testing.T) {
	document := `query Read { viewer { login } }
	mutation First { a: mergePullRequest(input: {pullRequestId: "PR_1"}) { clientMutationId } }
	mutation Second { b: mergePullRequest(input: {pullRequestId: "PR_2"}) { clientMutationId } }`
	for _, selected := range []string{"Read", "First", "Second"} {
		t.Run(selected, func(t *testing.T) {
			result := normalizeDocument(t, document, "-f", "operationName="+selected)
			seen := map[string]bool{}
			for _, op := range result.Operations {
				if op.Origin.OpType != "mutation" {
					continue
				}
				seen[op.Origin.Operation] = true
				want := model.ExecutionNotSelected
				if op.Origin.Operation == selected {
					want = model.ExecutionWillRun
				}
				if op.Execution != want || (want == model.ExecutionNotSelected && op.Evidence == "") {
					t.Fatalf("non-selected mutation must remain visible with proof: %+v", op)
				}
			}
			if !seen["First"] || !seen["Second"] || result.Classification.MutationFree != (selected == "Read") {
				t.Fatalf("wrong operation selection: %+v", result)
			}
		})
	}
	requireUnknown(t, normalizeDocument(t, document), model.UnknownOperationAmbig)
	requireUnknown(t, normalizeDocument(t, document, "-f", "operationName=Absent"), model.UnknownMalformed)
	requireUnknown(t, normalizeDocument(t, `query Read { viewer { login } }`, "-f", "operationName=Absent"), model.UnknownMalformed)
}

func TestNormalizeGraphQLUnknownMutations(t *testing.T) {
	for _, tc := range []struct {
		field  string
		detail string
	}{
		{"createDiscussion", "known GitHub mutation without semantic mapping"},
		{"notInAnyGitHubSchema", "not in schema snapshot"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			result := normalizeDocument(t, `mutation { `+tc.field+`(input: {}) { clientMutationId } }`)
			op := onlyOperation(t, result)
			if op.Family != model.FamilyUnknown || op.Action != "graphql.mutation."+tc.field || op.Effect != model.EffectWrite || op.UnknownReason != model.UnknownMutation || !strings.Contains(op.Detail, tc.detail) {
				t.Fatalf("unknown mutation identity/detail: %+v", op)
			}
			if result.Classification.Confidence != model.ConfidenceExact || result.Classification.MutationFree {
				t.Fatalf("AST-certain unknown mutation must remain a write: %+v", result.Classification)
			}
		})
	}
}

func TestNormalizeGraphQLInvalidAndUnavailableRequests(t *testing.T) {
	fileError := model.Invocation{ReadFile: func(string) ([]byte, error) { return nil, errors.New("unavailable fixture") }}
	cases := []struct {
		name   string
		args   []string
		inv    model.Invocation
		reason model.UnknownReason
	}{
		{"missing query", []string{"graphql"}, model.Invocation{}, model.UnknownMalformed},
		{"malformed GraphQL", []string{"graphql", "-f", "query=mutation {"}, model.Invocation{}, model.UnknownParseError},
		{"missing query file reader", []string{"graphql", "-F", "query=@query.graphql"}, model.Invocation{}, model.UnknownInputUnavailable},
		{"unreadable query file", []string{"graphql", "-F", "query=@query.graphql"}, fileError, model.UnknownInputUnavailable},
		{"query stdin not captured", []string{"graphql", "-F", "query=@-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinNotCaptured}}, model.UnknownInputUnavailable},
		{"query stdin too large", []string{"graphql", "-F", "query=@-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinTooLarge}}, model.UnknownInputTooLarge},
		{"query stdin timeout", []string{"graphql", "-F", "query=@-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinTimeout}}, model.UnknownInputUnavailable},
		{"input missing reader", []string{"graphql", "--input", "body.json"}, model.Invocation{}, model.UnknownInputUnavailable},
		{"input unreadable file", []string{"graphql", "--input", "body.json"}, fileError, model.UnknownInputUnavailable},
		{"input stdin not captured", []string{"graphql", "--input", "-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinNotCaptured}}, model.UnknownInputUnavailable},
		{"input stdin too large", []string{"graphql", "--input", "-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinTooLarge}}, model.UnknownInputTooLarge},
		{"input stdin timeout", []string{"graphql", "--input", "-"}, model.Invocation{Stdin: model.StdinData{Status: model.StdinTimeout}}, model.UnknownInputUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireUnknown(t, normalizeArgs(t, tc.inv, tc.args...), tc.reason)
		})
	}
}

func TestNormalizeGraphQLJSONValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		reason model.UnknownReason
	}{
		{"invalid JSON", `{"query":`, model.UnknownParseError},
		{"trailing JSON", `{"query":"{ viewer { login } }"} {}`, model.UnknownParseError},
		{"batch", `[{"query":"{ viewer { login } }"}]`, model.UnknownMalformed},
		{"null body", `null`, model.UnknownMalformed},
		{"missing document", `{"variables":{}}`, model.UnknownMalformed},
		{"non-string document", `{"query":true}`, model.UnknownMalformed},
		{"non-object variables", `{"query":"query { viewer { login } }","variables":[1]}`, model.UnknownMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: []byte(tc.body)}}
			requireUnknown(t, normalizeArgs(t, inv, "graphql", "--input", "-"), tc.reason)
		})
	}
}

func TestNormalizeGraphQLParseErrorHasLocation(t *testing.T) {
	result := normalizeDocument(t, "mutation Merge {\n mergePullRequest(input: )\n}")
	requireUnknown(t, result, model.UnknownParseError)
	if len(result.Errors) == 0 {
		t.Fatalf("parser errors must be exposed: %+v", result)
	}
	joined := strings.Join(result.Errors, " ")
	if !strings.Contains(joined, "2:") && !strings.Contains(joined, "2,") && !strings.Contains(joined, "line 2") {
		t.Fatalf("parser error missing line/column location: %q", joined)
	}
}

func TestNormalizeGraphQLSubscriptionIsUnknown(t *testing.T) {
	requireUnknown(t, normalizeDocument(t, `subscription Events { events { id } }`), model.UnknownNone)
}

func TestNormalizeGraphQLInputLimits(t *testing.T) {
	oversized := `query { viewer { login } } #` + strings.Repeat("x", 2<<20)
	deep := `query ` + strings.Repeat("{", 100000) + strings.Repeat("}", 100000)
	for name, document := range map[string]string{"oversized": oversized, "deep nesting": deep} {
		t.Run(name, func(t *testing.T) {
			requireUnknown(t, normalizeDocument(t, document), model.UnknownInputTooLarge)
		})
	}
	inv := model.Invocation{ReadFile: func(string) ([]byte, error) { return []byte(oversized), nil }}
	requireUnknown(t, normalizeArgs(t, inv, "graphql", "-F", "query=@large.graphql"), model.UnknownInputTooLarge)
	body, err := json.Marshal(map[string]any{"query": oversized})
	if err != nil {
		t.Fatal(err)
	}
	inv.Stdin = model.StdinData{Status: model.StdinCaptured, Data: body}
	requireUnknown(t, normalizeArgs(t, inv, "graphql", "--input", "-"), model.UnknownInputTooLarge)
}

func TestNormalizeGraphQLSourceIsBounded(t *testing.T) {
	document := `query { viewer { login } } #` + strings.Repeat("x", 128<<10)
	result := normalizeDocument(t, document)
	if !result.Classification.MutationFree || result.Source.GraphQL == "" || len(result.Source.GraphQL) >= len(document) || !strings.HasPrefix(document, result.Source.GraphQL) {
		t.Fatalf("source should retain a bounded document prefix: classification=%+v source bytes=%d document bytes=%d", result.Classification, len(result.Source.GraphQL), len(document))
	}
}

func TestNormalizeGraphQLHostAndExplicitGET(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		flagHost string
		envHost  string
		wantHost string
	}{
		{"environment", "graphql", "", "env.example", "env.example"},
		{"flag beats environment", "graphql", "flag.example", "env.example", "flag.example"},
		{"absolute public URL", "https://api.github.com/graphql", "flag.example", "env.example", "github.com"},
		{"absolute public case and HTTPS port", "https://API.GITHUB.COM:443/graphql", "", "", "github.com"},
		{"absolute public HTTP port", "http://API.GITHUB.COM:80/graphql", "", "", "github.com"},
		{"absolute tenancy URL", "https://API.TENANT.GHE.COM:443/graphql", "", "", "tenant.ghe.com"},
		{"absolute GHES URL", "https://GHES.EXAMPLE:443/api/graphql", "flag.example", "env.example", "ghes.example"},
		{"absolute GHES custom port", "https://GHES.EXAMPLE:8443/api/graphql", "", "", "ghes.example:8443"},
		{"GHES API prefix is part of host", "https://API.GHES.EXAMPLE/api/graphql", "", "", "api.ghes.example"},
		{"hostname public API host", "graphql", "API.GITHUB.COM:443", "env.example", "github.com"},
		{"hostname tenancy API host", "graphql", "API.TENANT.GHE.COM:443", "", "tenant.ghe.com"},
		{"environment public API host", "graphql", "", "API.GITHUB.COM:80", "github.com"},
		{"environment GHES host", "graphql", "", "GHES.EXAMPLE:443", "ghes.example"},
		{"hostname custom port", "graphql", "GHES.EXAMPLE:8443", "", "ghes.example:8443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{tc.endpoint, "-f", "query=" + mergeDocument, "-f", "id=PR_1"}
			if tc.flagHost != "" {
				args = append(args, "--hostname", tc.flagHost)
			}
			result := normalizeArgs(t, model.Invocation{Env: map[string]string{"GH_HOST": tc.envHost}}, args...)
			if onlyOperation(t, result).Target.Host != tc.wantHost || result.Source.Host != tc.wantHost {
				t.Fatalf("host precedence: %+v", result)
			}
		})
	}
	result := normalizeDocument(t, mergeDocument, "-f", "id=PR_1", "-X", "GET")
	op := onlyOperation(t, result)
	if op.Effect != model.EffectWrite || result.Classification.MutationFree || result.Source.Method != "GET" {
		t.Fatalf("explicit GET must not make a mutation safe: %+v", result)
	}
}

func TestNormalizeGraphQLRelativeEndpointRouting(t *testing.T) {
	for _, endpoint := range []string{"/graphql", "graphql/"} {
		for _, tc := range []struct {
			name, flagHost, envHost, wantHost string
			unknown                           bool
		}{
			{"public", "API.GITHUB.COM:443", "", "github.com", false},
			{"tenancy", "", "API.TENANT.GHE.COM:443", "tenant.ghe.com", false},
			{"tenancy root", "GHE.COM", "", "ghe.com", false},
			{"GHES hostname", "GHES.EXAMPLE", "", "ghes.example", true},
			{"GHES environment", "", "GHES.EXAMPLE:443", "ghes.example", true},
			{"not a tenancy host", "tenant.ghe.com.example", "", "tenant.ghe.com.example", true},
			{"unknown host", "", "", "", false},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				// Bypass the shared dispatcher; its relative routing change is
				// integrated separately by the lead engineer.
				req := model.APIRequest{Endpoint: endpoint, Method: "POST", Hostname: tc.flagHost, Fields: []model.APIField{
					{Key: "query", Value: mergeDocument},
					{Key: "id", Value: "ungrouped-node"},
				}}
				inv := model.Invocation{Env: map[string]string{"GH_HOST": tc.envHost}}
				result := graphql.Normalize(inv, req)
				if result.Source.Host != tc.wantHost {
					t.Fatalf("relative endpoint host normalization: %+v", result)
				}
				if tc.unknown {
					requireUnknown(t, result, model.UnknownEndpoint)
					if !strings.Contains(onlyOperation(t, result).Detail, "/api/v3/graphql") {
						t.Fatalf("GHES route mismatch needs an explanation: %+v", result)
					}
					req.Fields[0].Value = `{ viewer { login } }`
					requireUnknown(t, graphql.Normalize(inv, req), model.UnknownEndpoint)
					return
				}
				op := onlyOperation(t, result)
				if op.Target.Host != tc.wantHost || op.Action != "pr.merge.direct" || op.Target.NodeID != "" || !hasUnresolved(op, "variables.id", "variable") || !strings.Contains(op.Detail, "does not group") {
					t.Fatalf("relative fields must not become variables: %+v", result)
				}
				if tc.wantHost == "" && !strings.Contains(op.Detail, "host is unresolved") {
					t.Fatalf("unknown host needs a routing uncertainty note: %+v", result)
				}
				req.Fields = append(req.Fields, model.APIField{Key: "variables[id]", Value: "body-node"})
				if onlyOperation(t, graphql.Normalize(inv, req)).Target.NodeID != "body-node" {
					t.Fatal("relative request should accept an explicit body variables object")
				}
			})
		}
	}
	req := model.APIRequest{Endpoint: "graphql", Method: "POST", Hostname: "GHES.EXAMPLE", Fields: []model.APIField{
		{Key: "query", Value: mergeDocument},
		{Key: "id", Value: "literal-node"},
	}}
	op := onlyOperation(t, graphql.Normalize(model.Invocation{}, req))
	if op.Action != "pr.merge.direct" || op.Target.NodeID != "literal-node" || op.Target.Host != "ghes.example" {
		t.Fatalf("literal graphql must retain GHES routing and variable grouping: %+v", op)
	}
}

func TestNormalizeGraphQLVariablesDefaultsAndUnresolved(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
		fields   []string
		wantID   string
	}{
		{"variable", mergeDocument, []string{"-f", "id=PR_request"}, "PR_request"},
		{"variable default", `mutation($id: ID = "PR_default") { mergePullRequest(input: {pullRequestId: $id}) { clientMutationId } }`, nil, "PR_default"},
		{"request overrides default", `mutation($id: ID = "PR_default") { mergePullRequest(input: {pullRequestId: $id}) { clientMutationId } }`, []string{"-f", "id=PR_request"}, "PR_request"},
		{"nested typed input", `mutation($input: MergePullRequestInput!) { mergePullRequest(input: $input) { clientMutationId } }`, []string{"-F", "input[pullRequestId]=PR_nested", "-F", "input[mergeMethod]=SQUASH"}, "PR_nested"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeDocument(t, tc.document, tc.fields...)
			if onlyOperation(t, result).Target.NodeID != tc.wantID || result.Classification.MutationFree {
				t.Fatalf("variable resolution: %+v", result)
			}
		})
	}
	result := normalizeDocument(t, mergeDocument)
	op := onlyOperation(t, result)
	if op.Target.NodeID != "" || !hasUnresolved(op, "variables.id", "variable") || op.Effect != model.EffectWrite || result.Classification.MutationFree {
		t.Fatalf("unresolved variable must preserve mutation identity and uncertainty: %+v", result)
	}
	result = normalizeDocument(t, `mutation($input: MergePullRequestInput!) { mergePullRequest(input: $input) { clientMutationId } }`)
	if !hasUnresolved(onlyOperation(t, result), "variables.input", "variable") || result.Classification.MutationFree {
		t.Fatalf("unresolved whole input must be recorded: %+v", result)
	}
}

func TestNormalizeGraphQLArgumentValues(t *testing.T) {
	document := `mutation($input: FutureInput!) { futureMutation(input: $input) { clientMutationId } }`
	for _, typed := range []bool{false, true} {
		name, flag := "raw", "-f"
		want := map[string]any{"count": "17", "enabled": "true", "nothing": "null"}
		if typed {
			name, flag = "typed", "-F"
			want = map[string]any{"count": 17, "enabled": true, "nothing": nil}
		}
		t.Run(name, func(t *testing.T) {
			result := normalizeDocument(t, document, flag, "input[count]=17", flag, "input[enabled]=true", flag, "input[nothing]=null")
			op := onlyOperation(t, result)
			for key, value := range want {
				if !reflect.DeepEqual(op.Parameters[key], value) {
					t.Errorf("parameter %s: want %#v (%T), got %#v (%T)", key, value, value, op.Parameters[key], op.Parameters[key])
				}
			}
		})
	}

	literal := `mutation {
		futureMutation(input: {
			nested: {enabled: true, missing: null, mode: SQUASH}
			items: [{name: "one", count: 7}, {name: "two", count: -2}]
			ratio: 1.25
		}) { clientMutationId }
	}`
	op := onlyOperation(t, normalizeDocument(t, literal))
	want := map[string]any{
		"nested": map[string]any{"enabled": true, "missing": nil, "mode": "SQUASH"},
		"items": []any{
			map[string]any{"name": "one", "count": 7},
			map[string]any{"name": "two", "count": -2},
		},
		"ratio": 1.25,
	}
	gotJSON, err := json.Marshal(op.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("nested literal objects/lists/enums/numbers not preserved: want %s, got %s", wantJSON, gotJSON)
	}

	result := normalizeDocument(t, document,
		"-F", "input[nested][enabled]=true",
		"-F", "input[items][][name]=one",
		"-F", "input[items][][count]=7")
	op = onlyOperation(t, result)
	if !reflect.DeepEqual(op.Parameters["nested"], map[string]any{"enabled": true}) ||
		!reflect.DeepEqual(op.Parameters["items"], []any{map[string]any{"name": "one", "count": 7}}) {
		t.Fatalf("nested CLI variable input not preserved: %+v", op.Parameters)
	}
}

func TestNormalizeGraphQLVariableSecretsStayOutOfSource(t *testing.T) {
	document := `mutation Merge($id: ID!, $headline: String!, $body: String!, $email: String!) {
		mergePullRequest(input: {pullRequestId: $id, mergeMethod: SQUASH, commitHeadline: $headline, commitBody: $body, authorEmail: $email}) { clientMutationId }
	}`
	secrets := map[string]string{
		"id":       "PR_private_node_82a0",
		"headline": "ghp" + "_private_headline_9aa8", // split so secret scanners don't flag the fake
		"body":     "private commit body 281a5",
		"email":    "private-author-93aa@example.test",
	}
	args := []string{"graphql", "-f", "query=" + document}
	for _, key := range []string{"id", "headline", "body", "email"} {
		args = append(args, "-f", key+"="+secrets[key])
	}
	body, err := json.Marshal(map[string]any{"query": document, "variables": secrets})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"fields", "JSON"} {
		t.Run(source, func(t *testing.T) {
			var result model.Result
			if source == "fields" {
				result = normalizeArgs(t, model.Invocation{}, args...)
			} else {
				result = normalizeArgs(t, model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured, Data: body}}, "graphql", "--input", "-")
			}
			op := onlyOperation(t, result)
			if op.Target.NodeID != secrets["id"] || op.Parameters["commitHeadline"] != secrets["headline"] || op.Parameters["commitBody"] != secrets["body"] || op.Parameters["authorEmail"] != secrets["email"] {
				t.Fatalf("normalization must preserve variable values for downstream redaction: %+v", op)
			}
			serialized, err := json.Marshal(result.Source)
			if err != nil {
				t.Fatal(err)
			}
			for key, secret := range secrets {
				if strings.Contains(string(serialized), secret) {
					t.Errorf("variable %s leaked into Source", key)
				}
			}
			if result.Source.GraphQL != document {
				t.Fatal("source should retain variable references, not substitute values")
			}
		})
	}
}

func TestNormalizeGraphQLMappedMutationSemantics(t *testing.T) {
	for _, tc := range []struct {
		field  string
		input  string
		family string
		action string
		nodeID string
	}{
		{"enablePullRequestAutoMerge", `{pullRequestId: "PR_1", mergeMethod: SQUASH}`, "pr.merge", "pr.merge.auto.enable", "PR_1"},
		{"disablePullRequestAutoMerge", `{pullRequestId: "PR_1"}`, "pr.merge", "pr.merge.auto.disable", "PR_1"},
		{"enqueuePullRequest", `{pullRequestId: "PR_1", jump: true}`, "pr.merge", "pr.merge.queue.enqueue", "PR_1"},
		{"dequeuePullRequest", `{id: "PR_1"}`, "pr.merge", "pr.merge.queue.dequeue", "PR_1"},
		{"addPullRequestReview", `{pullRequestId: "PR_1", event: APPROVE}`, "pr.review", "pr.review.approve", "PR_1"},
		{"addPullRequestReview", `{pullRequestId: "PR_1", event: REQUEST_CHANGES}`, "pr.review", "pr.review.request-changes", "PR_1"},
		{"addPullRequestReview", `{pullRequestId: "PR_1", event: COMMENT}`, "pr.review", "pr.review.comment", "PR_1"},
		{"addPullRequestReview", `{pullRequestId: "PR_1"}`, "pr.review", "pr.review.pending", "PR_1"},
		{"createPullRequest", `{repositoryId: "REPO_1", baseRefName: "main", headRefName: "feature"}`, "pr.create", "pr.create", "REPO_1"},
		{"closePullRequest", `{pullRequestId: "PR_1"}`, "pr.close", "pr.close", "PR_1"},
		{"deleteRef", `{refId: "REF_1"}`, "ref.delete", "ref.delete", "REF_1"},
		{"createIssue", `{repositoryId: "REPO_1", title: "issue"}`, "issue.create", "issue.create", "REPO_1"},
		{"closeIssue", `{issueId: "ISSUE_1"}`, "issue.close", "issue.close", "ISSUE_1"},
		{"reopenIssue", `{issueId: "ISSUE_1"}`, "issue.reopen", "issue.reopen", "ISSUE_1"},
		{"deleteIssue", `{issueId: "ISSUE_1"}`, "issue.delete", "issue.delete", "ISSUE_1"},
		{"addStar", `{starrableId: "REPO_1"}`, "star", "star.add", "REPO_1"},
		{"removeStar", `{starrableId: "REPO_1"}`, "star", "star.remove", "REPO_1"},
	} {
		t.Run(tc.field+"/"+tc.action, func(t *testing.T) {
			result := normalizeDocument(t, `mutation { `+tc.field+`(input: `+tc.input+`) { clientMutationId } }`)
			op := onlyOperation(t, result)
			if op.Family != tc.family || op.Action != tc.action || op.Target.NodeID != tc.nodeID || op.Effect != model.EffectWrite || op.UnknownReason != model.UnknownNone || result.Classification.MutationFree {
				t.Fatalf("mapped mutation semantics: %+v", result)
			}
			if tc.field == "createPullRequest" && (op.Target.BaseBranch != "main" || op.Target.HeadBranch != "feature") {
				t.Fatalf("PR branch targets: %+v", op.Target)
			}
		})
	}
	result := normalizeDocument(t, `mutation { mergeBranch(input: {repositoryId: "REPO_1", base: "main", head: "feature"}) { clientMutationId } }`)
	op := onlyOperation(t, result)
	if op.Family != "branch.merge" || op.Action != "branch.merge" || op.Effect != model.EffectWrite {
		t.Fatalf("mergeBranch must never be classified as PR merge: %+v", op)
	}
	result = normalizeDocument(t, `mutation { mergePullRequest(input: {pullRequestId: "PR_1", expectedHeadOid: "abc123", mergeMethod: SQUASH}) { clientMutationId } }`)
	if onlyOperation(t, result).Target.SHA != "abc123" {
		t.Fatalf("expected head SHA missing: %+v", result)
	}
}

func FuzzNormalizeGraphQL(f *testing.F) {
	for _, seed := range []struct{ document, name string }{
		{`{ viewer { login } }`, ""},
		{mergeDocument, "Merge"},
		{`mutation { futureMutation(input: {}) @skip(if: true) { id } }`, ""},
		{`query Read { viewer { login } } mutation Write { futureMutation(input: {}) { id } }`, "Read"},
		{`mutation { ...A } fragment A on Mutation { ...A }`, ""},
		{`query { ...Missing }`, ""},
		{`mutation {`, "missing"},
		{strings.Repeat("{", 100000), ""},
		{"\x00\xff", "\x00"},
	} {
		f.Add(seed.document, seed.name)
	}
	f.Fuzz(func(t *testing.T, document, operationName string) {
		req := model.APIRequest{Endpoint: "graphql", Method: "POST", Fields: []model.APIField{{Key: "query", Value: document}, {Key: "operationName", Value: operationName}}}
		result := graphql.Normalize(model.Invocation{}, req)
		if !result.Classification.MutationFree {
			return
		}
		if result.Classification.Confidence == model.ConfidenceNone || len(result.Operations) == 0 {
			t.Fatalf("mutation-free classification has no known operation proof: %+v", result)
		}
		for _, op := range result.Operations {
			if op.Effect == model.EffectUnknown {
				t.Fatalf("unknown-effect request marked mutation-free: %+v", result)
			}
			if op.Effect == model.EffectWrite && op.Execution != model.ExecutionSkipped && op.Execution != model.ExecutionNotSelected {
				t.Fatalf("unproven write marked mutation-free: %+v", result)
			}
			if op.Effect == model.EffectWrite && op.Evidence == "" {
				t.Fatalf("skipped/non-selected write needs proof: %+v", op)
			}
		}
	})
}

func BenchmarkNormalizeGraphQLMerge(b *testing.B) {
	req := model.APIRequest{Endpoint: "graphql", Method: "POST", Fields: []model.APIField{{Key: "query", Value: mergeDocument}, {Key: "id", Value: "PR_node"}}}
	inv := model.Invocation{Env: map[string]string{"GH_HOST": "github.com"}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result := graphql.Normalize(inv, req)
		if len(result.Operations) != 1 || result.Operations[0].Action != "pr.merge.direct" {
			b.Fatalf("unexpected normalized mutation: %+v", result)
		}
	}
}

func TestNormalizeGraphQLUnavailableVariablesDoNotUseDefaults(t *testing.T) {
	document := `mutation($id: ID = "default-node") { mergePullRequest(input: {pullRequestId: $id}) { clientMutationId } }`
	result := normalizeArgs(t, model.Invocation{}, "graphql", "-f", "query="+document, "-F", "id=@missing")
	op := onlyOperation(t, result)
	if op.Target.NodeID != "" || !hasUnresolved(op, "variables.id", "variable") || result.Classification.MutationFree {
		t.Fatalf("unavailable supplied variable must not use its default: %+v", result)
	}
	document = `mutation($input: MergePullRequestInput!) { mergePullRequest(input: $input) { clientMutationId } }`
	result = normalizeArgs(t, model.Invocation{}, "graphql", "-f", "query="+document, "-F", "input[pullRequestId]=known-node", "-F", "input[mergeMethod]=@missing")
	op = onlyOperation(t, result)
	if op.Target.NodeID != "known-node" || !hasUnresolved(op, "variables.input", "variable") || op.Parameters["method"] != nil || result.Classification.MutationFree {
		t.Fatalf("partially unavailable input must retain known fields without guessing: %+v", result)
	}
}
