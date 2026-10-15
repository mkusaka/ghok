//go:build unix

package e2e

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
)

func TestWrapperExecFailureLog(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
	})
	if err := os.WriteFile(s.real, []byte("not an executable\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.gh(nil, "version").code(t, 126)
	s.absentRecord()
	records := s.logs()
	last := records[len(records)-1]
	if last.Decision != "exec-error" || last.Exec == nil || last.Exec.StartError == "" || last.Exec.ExitCode != 126 {
		t.Fatalf("failed exec log=%+v", last)
	}
	if len(records) != 2 || records[0].InvocationID != last.InvocationID {
		t.Fatalf("expected pre-exec and failure records for one invocation: %+v", records)
	}
}

func TestWrapperResolutionDemand(t *testing.T) {
	for _, tc := range []struct {
		name, phase, repository, api, response string
		rule, disabled, unrelated              bool
		wantLookup, wantHook                   bool
		wantDiagnostic                         bool
	}{
		{name: "observe-pre-match", phase: "pre", repository: "acme/project", wantLookup: true, wantHook: true},
		{name: "observe-pre-other-repo", phase: "pre", repository: "other/project", wantLookup: true},
		{name: "observe-post-match", phase: "post", repository: "acme/project", wantLookup: true, wantHook: true},
		{name: "observe-audit-rule", rule: true, repository: "acme/project", wantLookup: true},
		{name: "never-keeps-maybe-match", phase: "pre", api: "never", wantHook: true},
		{name: "failed-keeps-maybe-match", phase: "pre", response: `{"data":{"node":null}}`, wantLookup: true, wantHook: true, wantDiagnostic: true},
		{name: "disabled", phase: "pre", disabled: true},
		{name: "unrelated-operation", phase: "pre", unrelated: true},
		{name: "logging-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.env["GH_HOST"] = "github.com"
			s.env["FAKEGH_API_RESPONSE"] = `{"data":{"node":{"__typename":"PullRequest","number":7,"repository":{"nameWithOwner":"` + tc.repository + `"}}}}`
			if tc.response != "" {
				s.env["FAKEGH_API_RESPONSE"] = tc.response
			}
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				if tc.api != "" {
					c.Runtime.ResolveAPI = tc.api
				}
				match := config.Match{Repositories: config.StringList{"acme/*"}}
				if tc.rule {
					c.Policy.Rules = []config.Rule{{ID: "audit", Match: match, Decision: "audit"}}
				}
				if tc.phase != "" {
					h := s.hook("selected", tc.phase)
					h.Match = match
					if tc.disabled {
						no := false
						h.Enabled = &no
					}
					if tc.unrelated {
						h.Match.Operations = config.StringList{"issue.close"}
					}
					c.Hooks = []config.Hook{h}
				}
			})
			args := []string{"api", "graphql", "-f", `query=mutation { mergePullRequest(input:{pullRequestId:"PR_test_node"}) { clientMutationId } }`}
			s.gh(nil, args...).code(t, 0)
			records := s.records()
			want := 1
			if tc.wantLookup {
				want++
			}
			if len(records) != want {
				t.Fatalf("real gh calls=%+v, want %d", records, want)
			}
			if tc.wantLookup {
				helper := envMap(records[0].Env)
				if len(records[0].Argv) < 2 || records[0].Argv[0] != "api" || records[0].Argv[1] != "graphql" || helper["GHOK_NESTED"] != "1" || helper["GHOK_NESTED_TOKEN"] == "" {
					t.Fatalf("resolver helper=%+v", records[0])
				}
				if install.ValidNestedToken(filepath.Dir(s.cfg), helper) {
					t.Fatal("resolver token survived invocation")
				}
			}
			_, hookErr := os.Stat(filepath.Join(s.root, "selected.json"))
			if tc.wantHook {
				input := s.hookInput("selected")
				if tc.wantLookup && !tc.wantDiagnostic && input.Operations[0].Target.Repository != tc.repository {
					t.Fatalf("hook target=%+v", input.Operations[0].Target)
				}
			} else if !errors.Is(hookErr, os.ErrNotExist) {
				t.Fatalf("nonmatching hook ran: %v", hookErr)
			}
			logs := s.logs()
			if tc.wantDiagnostic && len(logs[len(logs)-1].Diagnostics) == 0 {
				t.Fatal("failed resolution omitted diagnostic")
			}
			if tc.rule && logs[len(logs)-1].Decision != "audit" {
				t.Fatalf("audit decision=%+v", logs)
			}
			if child := envMap(records[len(records)-1].Env); child["GHOK_NESTED_TOKEN"] != "" {
				t.Fatal("helper token leaked into user command")
			}
		})
	}
}

func TestWrapperReselectsSpecializedHooks(t *testing.T) {
	s := newSandbox(t)
	s.env["GH_HOST"] = "github.com"
	s.env["FAKEGH_API_RESPONSE"] = `{"data":{"node":{"__typename":"PullRequest","number":7,"repository":{"nameWithOwner":"acme/project"}}}}`
	s.configure(func(c *config.Config) {
		generic := s.hook("generic", "pre")
		generic.Match = config.Match{Operations: config.StringList{"comment.create"}, Repositories: config.StringList{"acme/*"}}
		specialized := s.hook("specialized", "pre")
		specialized.Match = config.Match{Operations: config.StringList{"pr.comment.create"}, Repositories: config.StringList{"acme/*"}}
		c.Hooks = []config.Hook{generic, specialized}
	})
	s.gh(nil, "api", "graphql", "-f", `query=mutation { addComment(input:{subjectId:"PR_specialized",body:"hello"}) { clientMutationId } }`).code(t, 0)
	input := s.hookInput("specialized")
	if op := input.Operations[0]; op.Action != "pr.comment.create" || op.Target.Kind != "pullRequest" || op.Target.Repository != "acme/project" {
		t.Fatalf("specialized hook operation=%+v", op)
	}
	if _, err := os.Stat(filepath.Join(s.root, "generic.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("generic hook ran after specialization")
	}
}

func TestWrapperNestedCredentials(t *testing.T) {
	for _, credentials := range []string{"marker-only", "forged-token", "valid-token"} {
		t.Run(credentials, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				h := s.hook("pre", "pre")
				h.Env["FAKEGH_HOOK_DECISION"] = "deny"
				c.Hooks = []config.Hook{h}
				yes := true
				c.Logging.Enabled = &yes
			})
			s.env["GHOK_NESTED"], s.env["GHOK_DEPTH"] = "1", "2"
			switch credentials {
			case "valid-token":
				token, cleanup, err := install.NewNestedToken(filepath.Dir(s.cfg))
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				s.env["GHOK_NESTED_TOKEN"] = token
			case "forged-token":
				s.env["GHOK_NESTED_TOKEN"] = strings.Repeat("a", 64)
			}
			args := []string{"pr", "merge", "1", "-R", "acme/project"}
			got := s.gh(nil, args...)
			if credentials != "valid-token" {
				got.code(t, 1)
				s.absentRecord()
				s.hookInput("pre")
				return
			}
			got.code(t, 0)
			if child := envMap(s.onlyRecord(args, nil).Env); child["GHOK_DEPTH"] != "3" {
				t.Fatalf("nested depth=%s", child["GHOK_DEPTH"])
			}
			if _, err := os.Stat(filepath.Join(s.root, "pre.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("valid nested invocation ran hooks")
			}
			if logs := s.logs(); len(logs) != 1 || logs[0].Decision != "passthrough-nested" {
				t.Fatalf("nested logs=%+v", logs)
			}
		})
	}
}

func TestWrapperNestedHookLifecycle(t *testing.T) {
	for _, phase := range []string{"pre", "post"} {
		t.Run(phase, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				h := s.hook("nested", phase)
				h.Command = []string{"/bin/sh", "-c", `/bin/cat >/dev/null; gh version || exit; printf '{"decision":"allow"}'`}
				h.Env = map[string]string{"GHOK_NESTED_TOKEN": "cannot override invocation token"}
				c.Hooks = []config.Hook{h}
			})
			s.gh(nil, "version").code(t, 0)
			records := s.records()
			if len(records) != 2 {
				t.Fatalf("nested and user gh calls=%+v", records)
			}
			nestedIndex := 0
			if phase == "post" {
				nestedIndex = 1
			}
			helper := envMap(records[nestedIndex].Env)
			if helper["GHOK_NESTED"] != "1" || len(helper["GHOK_NESTED_TOKEN"]) != 64 || helper["GHOK_DEPTH"] != "2" {
				t.Fatalf("nested helper env=%v", helper)
			}
			if install.ValidNestedToken(filepath.Dir(s.cfg), helper) {
				t.Fatal("hook token survived wrapper exit/exec")
			}
			if child := envMap(records[1-nestedIndex].Env); child["GHOK_NESTED_TOKEN"] != "" {
				t.Fatal("helper token leaked to original gh invocation")
			}
		})
	}
}
