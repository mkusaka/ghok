//go:build unix

package delegates

import (
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
	"github.com/mkusaka/ghok/internal/policy"
)

func TestSelection(t *testing.T) {
	c, err := config.Parse([]byte(`version: 1
delegates:
  - id: native
    match: {effect: read, level: L1, operation: [pr.view, pr.checks, pr.list, pr.diff, run.view, run.list], env: {RELAY: "1"}}
    unless: [{operation: pr.checks, parameters: {watch: "true"}}]
    command: [relay, gh]
  - id: api
    match: {effect: read, level: L2, command: api, method: GET}
    command: [relay, gh]
  - id: later
    match: {effect: read, level: L1}
    command: [other]
`), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "view", "12", "-R", "acme/repo"}, "native"},
		{[]string{"pr", "checks", "12", "--watch"}, "later"},
		{[]string{"run", "list"}, "native"},
		{[]string{"api", "repos/acme/repo/pulls/12"}, "api"},
		{[]string{"api", "unknown/route"}, "api"},
		{[]string{"api", "repos/acme/repo/pulls/12", "-X", "HEAD"}, ""},
		{[]string{"api", "repos/acme/repo/pulls/12", "-f", "title=write"}, ""},
		{[]string{"api", "graphql", "-X", "GET", "-f", "query=mutation { deleteIssue(input:{id:\"x\"}) { clientMutationId } }"}, ""},
		{[]string{"api", "graphql", "-f", "query={ viewer { login } }"}, ""},
		{[]string{"api", "rate_limit", "-H", "X-HTTP-Method-Override: DELETE"}, ""},
		{[]string{"pr", "merge", "12"}, ""},
		{[]string{"pr", "view", "--unknown-new-flag"}, ""},
		{[]string{"an-extension", "read"}, ""},
	} {
		t.Run(joinArgs(tc.args), func(t *testing.T) {
			env := map[string]string{"RELAY": "1"}
			r := normalize.Normalize(model.Invocation{Argv: tc.args, Env: env})
			s := policy.Subject{Result: r, Argv: r.Source.Argv, Env: env}
			selected := Select(c, s)
			got := ""
			if selected != nil {
				got = selected.ID
			}
			if got != tc.want {
				t.Fatalf("selected=%s want=%s result=%+v", got, tc.want, r)
			}
			env["GHOK_NO_DELEGATE"] = "1"
			if Select(c, s) != nil {
				t.Fatal("recursion guard ignored")
			}
		})
	}
}

func joinArgs(args []string) string {
	name := ""
	for _, arg := range args {
		name += arg + " "
	}
	return name
}

func TestSelectionRequiresEntireReadOnlyRequest(t *testing.T) {
	c := &config.Config{Delegates: []config.Delegate{{ID: "read", Match: config.Match{Effects: config.StringList{"read"}}, Command: []string{"relay"}}}}
	r := normalize.Normalize(model.Invocation{Argv: []string{"pr", "view", "1"}})
	r.Operations = append(r.Operations, model.Operation{Effect: model.EffectWrite, Execution: model.ExecutionWillRun})
	if Select(c, policy.Subject{Result: r}) != nil {
		t.Fatal("read match hid a second writing operation")
	}
}
