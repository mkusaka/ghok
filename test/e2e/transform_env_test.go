//go:build unix

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

func envTransformFixture(s *sandbox, id string, env map[string]any) config.Transform {
	s.t.Helper()
	tr := transformFixture(s, id, nil)
	data, err := json.Marshal(map[string]any{"schemaVersion": 1, "env": env})
	if err != nil {
		s.t.Fatal(err)
	}
	tr.Env["FAKEGH_HOOK_RESPONSE"] = string(data)
	tr.Env["FAKEGH_HOOK_ENV_RECORD"] = filepath.Join(s.root, id+"-env.json")
	return tr
}

func recordedHookEnv(t *testing.T, s *sandbox, id string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.root, id+"-env.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	decodeJSON(t, data, &entries)
	return envMap(entries)
}

func TestTransformEnvExecution(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		name := "exec-fast-path"
		if supervised {
			name = "supervised"
		}
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			token := "ghs" + "_environment_fixture"
			s.env["REMOVE_ME"] = "original"
			s.configure(func(c *config.Config) {
				c.Transforms = []config.Transform{envTransformFixture(s, "credentials", map[string]any{"GH_TOKEN": token, "REMOVE_ME": nil, "Z_CONTEXT": "selected"})}
				if supervised {
					pre, post := s.hook("pre", "pre"), s.hook("post", "post")
					pre.Match = config.Match{Tokens: config.StringList{"app"}, Env: map[string]string{"Z_CONTEXT": "selected", "REMOVE_ME": ""}}
					pre.Env["FAKEGH_HOOK_ENV_RECORD"] = filepath.Join(s.root, "pre-env.json")
					post.Env["FAKEGH_HOOK_ENV_RECORD"] = filepath.Join(s.root, "post-env.json")
					c.Hooks = []config.Hook{pre, post}
				}
			})
			args := []string{"version"}
			s.management(nil, "exec", "--", "version").code(t, 0)
			env := envMap(s.onlyRecord(args, nil).Env)
			if env["GH_TOKEN"] != token || env["GHOK_DEPTH"] != "1" || env["Z_CONTEXT"] != "selected" {
				t.Fatalf("child environment missing edits or depth: %v", env)
			}
			if _, exists := env["REMOVE_ME"]; exists {
				t.Fatal("unset variable survived")
			}
			if supervised {
				for _, id := range []string{"pre", "post"} {
					in := s.hookInput(id)
					if in.Invocation.TokenKind != "app" || !reflect.DeepEqual(in.Invocation.EnvChanged, []string{"GH_TOKEN", "REMOVE_ME", "Z_CONTEXT"}) {
						t.Fatalf("%s input metadata: %+v", id, in.Invocation)
					}
				}
				pre, post := recordedHookEnv(t, s, "pre"), recordedHookEnv(t, s, "post")
				if pre["GH_TOKEN"] != "" || pre["REMOVE_ME"] != "original" || post["GH_TOKEN"] != token {
					t.Fatalf("PRE/POST process environments incorrect: pre=%v post=%v", pre, post)
				}
			}
		})
	}
}

func TestTransformEnvPolicy(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = "enforce"
		c.Transforms = []config.Transform{envTransformFixture(s, "credentials", map[string]any{"GH_TOKEN": "ghs" + "_policy_fixture"})}
		c.Policy.Rules = []config.Rule{{ID: "app-denied", Match: config.Match{Tokens: config.StringList{"app"}}, Decision: "deny"}}
	})
	got := s.gh(nil, "version")
	got.code(t, 77)
	s.absentRecord()
	if !strings.Contains(string(got.stderr), "denied by rule app-denied") {
		t.Fatalf("policy did not use transformed credentials: %s", got.stderr)
	}
}

func TestTransformEnvChain(t *testing.T) {
	s := newSandbox(t)
	s.env["CHAIN"] = "original"
	s.configure(func(c *config.Config) {
		never := envTransformFixture(s, "no-retrigger", map[string]any{"CHAIN": "wrong"})
		never.Match.Env = map[string]string{"CHAIN": "first"}
		first := envTransformFixture(s, "first", map[string]any{"CHAIN": "first", "GH_TOKEN": "ghs" + "_chain_fixture"})
		second := envTransformFixture(s, "second", map[string]any{"CHAIN": "last"})
		second.Match = config.Match{Tokens: config.StringList{"app"}, Env: map[string]string{"CHAIN": "first"}}
		c.Transforms = []config.Transform{never, first, second}
	})
	s.gh(nil, "version").code(t, 0)
	if envMap(s.onlyRecord([]string{"version"}, nil).Env)["CHAIN"] != "last" {
		t.Fatal("later matching transform did not win")
	}
	if got := recordedHookEnv(t, s, "second"); got["CHAIN"] != "first" || got["GH_TOKEN"] != "ghs"+"_chain_fixture" {
		t.Fatalf("transform process did not receive current environment: %v", got)
	}
	if _, err := os.Stat(filepath.Join(s.root, "no-retrigger.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("earlier transform retriggered: %v", err)
	}
}

func TestTransformEnvRollback(t *testing.T) {
	s := newSandbox(t)
	s.env["CHAIN"] = "original"
	s.configure(func(c *config.Config) {
		first := envTransformFixture(s, "environment", map[string]any{"CHAIN": "changed", "GH_TOKEN": "ghs" + "_rollback_fixture"})
		request := transformFixture(s, "request", map[string]any{"/operations/0/texts/body/value": "changed"})
		bad := envTransformFixture(s, "bad", map[string]any{"GHOK_DEPTH": "99"})
		bad.OnError = "allow"
		c.Transforms = []config.Transform{first, request, bad, envTransformFixture(s, "never", map[string]any{"CHAIN": "wrong"})}
		c.Hooks = []config.Hook{s.hook("inspect", "pre")}
	})
	args := []string{"issue", "comment", "12", "-R", "acme/project", "--body", "original"}
	s.gh(nil, args...).code(t, 0)
	env := envMap(s.onlyRecord(args, nil).Env)
	in := s.hookInput("inspect")
	if env["CHAIN"] != "original" || env["GH_TOKEN"] != "" || env["GHOK_DEPTH"] != "1" || in.Invocation.TokenKind != "none" || len(in.Invocation.EnvChanged) != 0 || in.Operations[0].Texts.Body.Value != "original" {
		t.Fatalf("partial chain survived rollback: env=%v input=%+v", env, in)
	}
	if _, err := os.Stat(filepath.Join(s.root, "never.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("chain continued after rollback: %v", err)
	}
}

func TestTransformEnvForbiddenName(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = mode
				c.Transforms = []config.Transform{envTransformFixture(s, "forbidden", map[string]any{"GHOK_DEPTH": "99"})}
			})
			got := s.gh(nil, "version")
			got.code(t, 78)
			s.absentRecord()
			if !strings.Contains(string(got.stderr), "denied by transform forbidden") || !strings.Contains(string(got.stderr), "(error: invalid-response)") {
				t.Fatalf("missing invalid-response deny contract: %s", got.stderr)
			}
		})
	}
}

func TestTransformEnvEvidenceAndRedaction(t *testing.T) {
	for _, command := range []string{"exec", "explain", "explain-secrets", "hooks-test", "hooks-test-secrets"} {
		t.Run(command, func(t *testing.T) {
			s := newSandbox(t)
			secret := "arbitrary-credential-" + "without-token-prefix"
			s.env["REMOVE_ME"] = "remove-value-never-report"
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				tr := envTransformFixture(s, "credentials", map[string]any{"GH_TOKEN": "ghs" + "_evidence_fixture", "PRIVATE_CONTEXT": secret, "REMOVE_ME": nil})
				setBytes(tr.Env, "FAKEGH_HOOK_STDERR_B64", []byte(secret))
				c.Transforms = []config.Transform{tr}
				h := s.hook("gate", "pre")
				h.Env["FAKEGH_HOOK_RESPONSE"] = `{"decision":"deny","message":"` + secret + `"}`
				c.Hooks = []config.Hook{h}
				c.Policy.Mode = "enforce"
			})
			var got outcome
			switch command {
			case "exec":
				got = s.gh(nil, "version")
				got.code(t, 77)
			case "explain", "explain-secrets":
				args := []string{"explain"}
				if command == "explain-secrets" {
					args = append(args, "--show-secrets")
				}
				got = s.management(nil, append(args, "--", "version")...)
				got.code(t, 0)
			default:
				args := []string{"hooks", "test", "--json", "--expect", "deny"}
				if command == "hooks-test-secrets" {
					args = append(args, "--show-secrets")
				}
				got = s.management(nil, append(args, "--", "version")...)
				got.code(t, 0)
			}
			s.absentRecord()
			var logs []byte
			if command == "exec" {
				var err error
				logs, err = json.Marshal(s.logs())
				if err != nil {
					t.Fatal(err)
				}
			}
			all := string(got.stdout) + string(got.stderr) + string(logs)
			for _, value := range []string{secret, "ghs" + "_evidence_fixture", "remove-value-never-report"} {
				if strings.Contains(all, value) {
					t.Fatalf("environment value leaked: %s", all)
				}
			}
			if command == "exec" {
				var records []struct {
					Transformed *struct {
						Env   []string          `json:"env"`
						EnvBy map[string]string `json:"envBy"`
					} `json:"transformed"`
				}
				decodeJSON(t, logs, &records)
				if len(records) != 1 || records[0].Transformed == nil || !reflect.DeepEqual(records[0].Transformed.Env, []string{"GH_TOKEN", "PRIVATE_CONTEXT", "REMOVE_ME"}) || records[0].Transformed.EnvBy["PRIVATE_CONTEXT"] != "credentials" {
					t.Fatalf("env-only transform missing JSONL metadata: %s", logs)
				}
				if !strings.Contains(string(got.stderr), "[REDACTED]") {
					t.Fatalf("deny diagnostic not redacted: %s", got.stderr)
				}
			} else {
				for _, evidence := range []string{"GH_TOKEN", "PRIVATE_CONTEXT", "REMOVE_ME", "credentials", "app"} {
					if !strings.Contains(string(got.stdout), evidence) {
						t.Fatalf("missing %s evidence: %s", evidence, got.stdout)
					}
				}
				if !strings.HasPrefix(command, "explain") {
					var report struct {
						Transformed struct {
							Env       []string          `json:"env"`
							EnvBy     map[string]string `json:"envBy"`
							EnvUnset  []string          `json:"envUnset"`
							TokenKind string            `json:"tokenKind"`
						} `json:"transformed"`
					}
					decodeJSON(t, got.stdout, &report)
					if !reflect.DeepEqual(report.Transformed.Env, []string{"GH_TOKEN", "PRIVATE_CONTEXT", "REMOVE_ME"}) || report.Transformed.EnvBy["GH_TOKEN"] != "credentials" || !reflect.DeepEqual(report.Transformed.EnvUnset, []string{"REMOVE_ME"}) || report.Transformed.TokenKind != "app" {
						t.Fatalf("incomplete management environment evidence: %s", got.stdout)
					}
				}
			}
		})
	}
}

func TestTransformRepositoryMatchRequiresKnownRepository(t *testing.T) {
	for _, tc := range []struct {
		name, repo string
		args       []string
		applied    bool
	}{
		{"repo-less api outside repo", "", []string{"api", "rate_limit"}, false},
		{"repo-less api in other repo", "other/project", []string{"api", "rate_limit"}, false},
		{"implicit repo in matched repo", "acme/project", []string{"pr", "list"}, true},
		{"implicit repo in other repo", "other/project", []string{"pr", "list"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			token := "ghs" + "_repository_fixture"
			if tc.repo != "" {
				s.env["GH_REPO"] = tc.repo
			}
			s.configure(func(c *config.Config) {
				tr := envTransformFixture(s, "credentials", map[string]any{"GH_TOKEN": token})
				tr.Match = config.Match{Repositories: config.StringList{"acme/*"}}
				c.Transforms = []config.Transform{tr}
			})
			s.gh(nil, tc.args...).code(t, 0)
			if got := envMap(s.onlyRecord(tc.args, nil).Env)["GH_TOKEN"] == token; got != tc.applied {
				t.Fatalf("transform applied = %v, want %v", got, tc.applied)
			}
		})
	}
}
