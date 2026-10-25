//go:build unix

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

// Every repositories criterion selects only operations gh runs against a
// matching repository. Repository-less requests (rate_limit, gists) never match;
// implicit-repository requests outside any repository stay conservative.
func TestRepositoryMatchAcrossFeatures(t *testing.T) {
	ops := map[string][]string{
		"repo-less-read":  {"api", "rate_limit"},
		"repo-less-write": {"api", "-X", "POST", "gists"},
		"implicit-read":   {"pr", "list"},
		"implicit-write":  {"pr", "merge", "1"},
		// GraphQL documents carry no repository evidence and keep using the cwd.
		"graphql-read": {"api", "graphql", "-f", `query={viewer{login}}`},
	}
	// Expected selection by op, then repository source: none, GH_REPO, cwd git remote.
	selected := map[string]map[string]bool{
		"repo-less-read":  {},
		"repo-less-write": {},
		"implicit-read":   {"none": true, "env-acme": true, "git-acme": true},
		"implicit-write":  {"none": true, "env-acme": true, "git-acme": true},
		"graphql-read":    {"none": true, "env-acme": true, "git-acme": true},
	}
	for _, kind := range []string{"transform", "rule", "pre", "post", "protected"} {
		for _, src := range []string{"none", "env-acme", "env-other", "git-acme", "git-other"} {
			for op, args := range ops {
				t.Run(kind+"/"+src+"/"+op, func(t *testing.T) {
					want := selected[op][src]
					switch {
					case kind == "transform" && src == "none":
						want = false // transforms require a definite match
					case kind == "protected":
						// Only unresolved writes are denied; known protected targets are not.
						want = op == "implicit-write" && src == "none"
					}
					s := newSandbox(t)
					repo, _ := strings.CutPrefix(src, "env-")
					if repo != src {
						s.env["GH_REPO"] = repo + "/project"
					} else if repo, ok := strings.CutPrefix(src, "git-"); ok {
						for _, c := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/" + repo + "/project.git"}} {
							cmd := exec.Command("git", c...)
							cmd.Dir = s.cwd
							if out, err := cmd.CombinedOutput(); err != nil {
								t.Fatalf("git %v: %v %s", c, err, out)
							}
						}
					}
					token := "ghs" + "_repository_fixture"
					s.configure(func(c *config.Config) {
						c.Policy.Mode = "enforce"
						c.Runtime.ResolveAPI = "never"
						m := config.Match{Repositories: config.StringList{"acme/*"}}
						switch kind {
						case "transform":
							tr := envTransformFixture(s, "credentials", map[string]any{"GH_TOKEN": token})
							tr.Match = m
							c.Transforms = []config.Transform{tr}
						case "rule":
							c.Policy.Rules = []config.Rule{{ID: "acme", Match: m, Decision: "deny"}}
						case "protected":
							c.Policy.ProtectedRepositories = []string{"acme/*"}
						default:
							h := s.hook("acme", kind)
							h.Match = m
							c.Hooks = []config.Hook{h}
						}
					})
					got := s.gh(nil, args...)
					var applied bool
					switch kind {
					case "transform":
						got.code(t, 0)
						applied = envMap(s.onlyRecord(args, nil).Env)["GH_TOKEN"] == token
					case "rule", "protected":
						var exit *exec.ExitError
						applied = errors.As(got.err, &exit) && exit.ExitCode() == 77
					default:
						got.code(t, 0)
						_, err := os.Stat(filepath.Join(s.root, "acme.json"))
						applied = err == nil
					}
					if applied != want {
						t.Fatalf("selected=%v, want %v; stderr=%s", applied, want, got.stderr)
					}
				})
			}
		}
	}
}
