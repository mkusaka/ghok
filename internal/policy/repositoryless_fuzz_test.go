package policy

import (
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
)

// FuzzRepositoryLess checks RepositoryLess against the normalizers: an operation
// whose target GH_REPO changes runs against a repository and must not be
// repository-less, and a repository-less operation never matches repositories.
func FuzzRepositoryLess(f *testing.F) {
	for _, s := range []string{
		"api\x00rate_limit", "api\x00-X\x00POST\x00gists", "api\x00repos/{owner}/{repo}/pulls", "api\x00user",
		"pr\x00list", "pr\x00merge\x001", "gist\x00create\x00x", "secret\x00list\x00--org\x00o", "search\x00repos\x00x",
		"api\x00graphql\x00-f\x00query={viewer{login}}", "repo\x00view", "issue\x00list\x00-R\x00o/r", "workflow\x00list",
	} {
		f.Add(s)
	}
	m := config.Match{Repositories: config.StringList{"*/*"}}
	f.Fuzz(func(t *testing.T, s string) {
		argv := strings.Split(s, "\x00")
		plain := normalize.Normalize(model.Invocation{Argv: argv, Env: map[string]string{}})
		scoped := normalize.Normalize(model.Invocation{Argv: argv, Env: map[string]string{"GH_REPO": "fuzz-owner/fuzz-repo"}})
		level := plain.Classification.Level
		for i, op := range plain.Operations {
			if !RepositoryLess(level, op) {
				continue
			}
			if len(scoped.Operations) == len(plain.Operations) && scoped.Operations[i].Action == op.Action && scoped.Operations[i].Target.Repository != "" {
				t.Fatalf("GH_REPO sets the repository of repository-less %s: %+v", op.Action, scoped.Operations[i].Target)
			}
			s := Subject{Result: model.Result{Classification: plain.Classification, Operations: []model.Operation{op}}, Argv: argv, Context: &model.ResolvedContext{Repository: "cwd-owner/cwd-repo", Host: "github.com"}}
			if yes, _ := Matches(m, nil, s); yes {
				t.Fatalf("repository-less %s matched repositories", op.Action)
			}
			if maybe, _ := MatchMaybe(m, nil, s); maybe {
				t.Fatalf("repository-less %s maybe-matched repositories", op.Action)
			}
		}
	})
}
