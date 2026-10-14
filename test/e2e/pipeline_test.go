//go:build unix

package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/logging"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestPreHookDecisions(t *testing.T) {
	for _, mode := range []string{"enforce", "observe"} {
		for _, decision := range []string{"allow", "deny", "abstain"} {
			t.Run(mode+"/"+decision, func(t *testing.T) {
				s := newSandbox(t)
				s.configure(func(c *config.Config) {
					c.Policy.Mode = mode
					h := s.hook("pre", "pre")
					h.Env["FAKEGH_HOOK_DECISION"] = decision
					h.Env["FAKEGH_HOOK_MESSAGE"] = "consumer supplied reason"
					c.Hooks = []config.Hook{h}
				})
				args := []string{"pr", "merge", "1", "-R", "acme/project"}
				result := s.gh(nil, args...)
				if mode == "enforce" && decision == "deny" {
					result.code(t, 1)
					s.absentRecord()
					if !bytes.Contains(result.stderr, []byte("ghok: denied:")) || !bytes.Contains(result.stderr, []byte("consumer supplied reason")) {
						t.Fatal("missing explicit denial/message")
					}
				} else {
					result.code(t, 0)
					s.onlyRecord(args, nil)
					if len(result.stderr) != 0 {
						t.Fatalf("allowed invocation emitted diagnostic: %s", result.stderr)
					}
				}
				input := s.hookInput("pre")
				if input.Event != "pre" || input.Result != nil || input.Policy == nil || input.Policy.Mode != mode {
					t.Fatalf("invalid pre protocol: %+v", input)
				}
				if input.Invocation.IsTTY || input.Invocation.StdinTTY {
					t.Fatal("pipe invocation marked as TTY")
				}
			})
		}
	}
}

func TestHookErrorsAndTimeout(t *testing.T) {
	for _, behavior := range []string{"timeout", "exit", "invalid-response"} {
		for _, onError := range []string{"deny", "allow", "ignore"} {
			t.Run(behavior+"/"+onError, func(t *testing.T) {
				s := newSandbox(t)
				s.configure(func(c *config.Config) {
					yes := true
					c.Logging.Enabled = &yes
					c.Policy.Mode = "enforce"
					h := s.hook("pre", "pre")
					h.OnError = onError
					switch behavior {
					case "timeout":
						h.Timeout = config.Duration(2 * time.Second)
						h.Env["FAKEGH_HOOK_SLEEP"] = "1m"
					case "exit":
						h.Env["FAKEGH_HOOK_EXIT"] = "23"
					case "invalid-response":
						h.Env["FAKEGH_HOOK_RESPONSE"] = "not JSON"
					}
					c.Hooks = []config.Hook{h}
				})
				result := s.gh(nil, "version")
				if onError == "deny" {
					result.code(t, 1)
					s.absentRecord()
				} else {
					result.code(t, 0)
					s.onlyRecord([]string{"version"}, nil)
				}
				s.hookInput("pre")
				logs := s.logs()
				wantOutcome := behavior
				if behavior == "exit" {
					wantOutcome = "error"
				}
				wantEffective := onError
				if onError == "ignore" {
					wantEffective = "abstain"
				}
				if len(logs) != 1 || len(logs[0].Hooks) != 1 || string(logs[0].Hooks[0].Outcome) != wantOutcome || string(logs[0].Hooks[0].Effective) != wantEffective {
					t.Fatalf("hook outcome log=%+v", logs)
				}
			})
		}
	}
}

func TestPostHooksCannotChangeStatus(t *testing.T) {
	for _, behavior := range []string{"deny", "exit", "timeout", "invalid-response"} {
		t.Run(behavior, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				c.Policy.Mode = "enforce"
				h := s.hook("post", "post")
				switch behavior {
				case "deny":
					h.Env["FAKEGH_HOOK_DECISION"] = "deny"
				case "exit":
					h.Env["FAKEGH_HOOK_EXIT"] = "33"
				case "timeout":
					h.Timeout = config.Duration(2 * time.Second)
					h.Env["FAKEGH_HOOK_SLEEP"] = "1m"
				case "invalid-response":
					h.Env["FAKEGH_HOOK_RESPONSE"] = "not JSON"
				}
				c.Hooks = []config.Hook{h}
			})
			s.env["FAKEGH_EXIT"] = "42"
			result := s.gh(nil, "version")
			result.code(t, 42)
			s.onlyRecord([]string{"version"}, nil)
			input := s.hookInput("post")
			if input.Event != "post" || input.Result == nil || input.Result.ExitCode != 42 {
				t.Fatalf("post result=%+v", input.Result)
			}
			logs := s.logs()
			wantOutcome := behavior
			if behavior == "exit" {
				wantOutcome = "error"
			}
			if behavior == "deny" {
				wantOutcome = "ok"
			}
			if len(logs) != 1 || len(logs[0].Hooks) != 1 || string(logs[0].Hooks[0].Outcome) != wantOutcome || logs[0].Hooks[0].Effective != protocol.DecisionAbstain {
				t.Fatalf("post outcome log=%+v", logs)
			}
		})
	}
}

func TestGraphQLStdinAndFile(t *testing.T) {
	query := `mutation { futureDanger(input: {value: "payload"}) { clientMutationId } }`
	payload, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"stdin", "input-file", "query-file"} {
		for _, deny := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deny=%v", source, deny), func(t *testing.T) {
				s := newSandbox(t)
				s.configure(func(c *config.Config) {
					c.Policy.Mode = "enforce"
					c.Policy.UnknownMutation = "allow"
					if deny {
						c.Policy.UnknownMutation = "deny"
					}
					c.Hooks = []config.Hook{s.hook("pre", "pre")}
				})
				var input []byte
				args := []string{"api", "graphql"}
				switch source {
				case "stdin":
					input = payload
					args = append(args, "--input", "-")
				case "input-file":
					if err := os.WriteFile(filepath.Join(s.cwd, "payload.json"), payload, 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--input", "payload.json")
				case "query-file":
					if err := os.WriteFile(filepath.Join(s.cwd, "query.graphql"), []byte(query), 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "-F", "query=@query.graphql")
				}
				result := s.gh(input, args...)
				if deny {
					result.code(t, 1)
					s.absentRecord()
				} else {
					result.code(t, 0)
					s.onlyRecord(args, input)
				}
				hook := s.hookInput("pre")
				if hook.Classification.Level != "L3" || hook.Classification.MutationFree || len(hook.Operations) != 1 || hook.Operations[0].Family != "unknown" || hook.Operations[0].Effect != "write" {
					t.Fatalf("GraphQL mutation not detected: %+v %+v", hook.Classification, hook.Operations)
				}
			})
		}
	}
}

func TestObservePolicyNeverBlocks(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
		c.Policy.Mode = "observe"
		c.Policy.UnknownMutation = "deny"
		c.Policy.Rules = []config.Rule{{ID: "merge", Match: config.Match{Operations: config.StringList{"pr.merge.*"}}, Decision: "deny"}}
		h := s.hook("pre", "pre")
		h.Env["FAKEGH_HOOK_DECISION"] = "deny"
		c.Hooks = []config.Hook{h}
	})
	args := []string{"pr", "merge", "1", "-R", "acme/project"}
	result := s.gh(nil, args...)
	result.code(t, 0)
	s.onlyRecord(args, nil)
	if len(result.stderr) != 0 {
		t.Fatal("observe printed enforcing diagnostic")
	}
	input := s.hookInput("pre")
	if input.Policy.Decision == protocol.DecisionDeny {
		t.Fatal("observe policy reported enforce deny")
	}
	if logs := s.logs(); len(logs) != 1 || logs[0].Decision != "would-deny" {
		t.Fatalf("observe decision log=%+v", logs)
	}
}

func TestProtectedUnresolvedFailsClosed(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) { c.Policy.Mode = "enforce"; c.Policy.ProtectedRepositories = []string{"acme/*"} })
	result := s.gh(nil, "pr", "merge", "1")
	result.code(t, 1)
	s.absentRecord()
	if got, want := string(result.stderr), "ghok: denied by policy: target repository unresolved; cannot prove it is not protected (pr.merge.request)\n"; got != want {
		t.Fatalf("protected denial=%q, want=%q", got, want)
	}
}

func TestPolicyDenyMessageReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		policy config.Policy
		hook   bool
		want   string
	}{
		{
			name: "rule",
			args: []string{"pr", "merge", "1", "--admin", "-R", "acme/project"},
			policy: config.Policy{Rules: []config.Rule{
				{ID: "audit", Decision: "audit", Reason: "audit-only diagnostic"},
				{ID: "no-admin", Match: config.Match{Operations: config.StringList{"pr.merge.direct"}}, Decision: "deny", Reason: "direct merges are forbidden"},
			}},
			want: "rule no-admin: direct merges are forbidden (pr.merge.direct)",
		},
		{
			name:   "unknown and protected",
			args:   []string{"api", "-X", "POST", "repos/acme/project/unknown"},
			policy: config.Policy{UnknownMutation: config.UnknownDeny, ProtectedRepositories: []string{"acme/*"}},
			want:   "unclassified-write: unknownMutation policy (api.post.unknown); unclassified-write targets protected repository acme/project (api.post.unknown)",
		},
		{
			name:   "policy and hook",
			args:   []string{"pr", "merge", "1", "--admin", "-R", "acme/project"},
			policy: config.Policy{Rules: []config.Rule{{ID: "no-admin", Decision: "deny", Reason: "direct merges are forbidden"}}},
			hook:   true,
			want:   "rule no-admin: direct merges are forbidden (pr.merge.direct); hook restriction; contact maintainers",
		},
	} {
		for _, mode := range []string{config.ModeEnforce, config.ModeObserve} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				s := newSandbox(t)
				s.configure(func(c *config.Config) {
					c.Policy = tc.policy
					c.Policy.Mode = mode
					if tc.hook {
						h := s.hook("pre", "pre")
						h.Env["FAKEGH_HOOK_RESPONSE"] = `{"decision":"deny","reason":"hook restriction","message":"contact maintainers"}`
						c.Hooks = []config.Hook{h}
					}
				})
				result := s.gh(nil, tc.args...)
				if mode == config.ModeObserve {
					result.code(t, 0)
					s.onlyRecord(tc.args, nil)
					if len(result.stderr) != 0 {
						t.Fatalf("observe emitted deny reasons: %s", result.stderr)
					}
				} else {
					result.code(t, 1)
					s.absentRecord()
					if got, want := string(result.stderr), "ghok: denied by policy: "+tc.want+"\n"; got != want {
						t.Fatalf("denial=%q, want=%q", got, want)
					}
				}
			})
		}
	}
}

func TestProtectedGraphQLUsesResolver(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprintf("resolved=%v", success), func(t *testing.T) {
			s := newSandbox(t)
			s.env["GH_HOST"] = "github.com"
			s.env["FAKEGH_API_RESPONSE"] = `{"data":{"node":{"__typename":"PullRequest","number":7,"repository":{"nameWithOwner":"acme/project"}}}}`
			if !success {
				s.env["FAKEGH_API_RESPONSE"] = `{"data":{"node":null}}`
			}
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				c.Policy.ProtectedRepositories = []string{"acme/*"}
				c.Hooks = []config.Hook{s.hook("pre", "pre")}
			})
			args := []string{"api", "graphql", "-f", `query=mutation { mergePullRequest(input:{pullRequestId:"PR_test_node"}) { clientMutationId } }`}
			result := s.gh(nil, args...)
			records := s.records()
			want := 1
			if success {
				want = 2
				result.code(t, 0)
			} else {
				result.code(t, 1)
			}
			if len(records) != want {
				t.Fatalf("resolver/user invocation count=%d, want=%d", len(records), want)
			}
			resolver := records[0]
			if len(resolver.Argv) < 2 || resolver.Argv[0] != "api" || resolver.Argv[1] != "graphql" || envMap(resolver.Env)["GHOK_NESTED"] != "1" {
				t.Fatalf("invalid resolver child: %+v", resolver)
			}
			if success {
				hook := s.hookInput("pre")
				if len(hook.Operations) != 1 || hook.Operations[0].Target.Repository != "acme/project" {
					t.Fatalf("resolved target=%+v", hook.Operations)
				}
			}
		})
	}
}

func TestResolverLazinessOnUnmatchedRead(t *testing.T) {
	s := newSandbox(t)
	// A malformed local git config would surface in context if Local ran.
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cwd, ".git", "config"), []byte("[invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	s.configure(func(c *config.Config) {
		c.Policy.Mode = "enforce"
		c.Policy.ProtectedRepositories = []string{"acme/*"}
		h := s.hook("unmatched", "pre")
		h.Match = config.Match{Operations: config.StringList{"pr.merge.*"}, Repositories: config.StringList{"acme/*"}}
		probe := s.hook("probe", "pre")
		c.Hooks = []config.Hook{h, probe}
	})
	args := []string{"api", "repos/acme/project/issues", "--method", "GET"}
	result := s.gh(nil, args...)
	result.code(t, 0)
	s.onlyRecord(args, nil)
	if input := s.hookInput("probe"); input.Context != nil {
		t.Fatalf("unmatched read unnecessarily resolved local context: %+v", input.Context)
	}
	if _, err := os.Stat(filepath.Join(s.root, "unmatched.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unmatched hook ran")
	}
}

func TestNestedRecursionProbeAndInvalidConfig(t *testing.T) {
	t.Run("nested", func(t *testing.T) {
		s := newSandbox(t)
		s.configure(func(c *config.Config) {
			c.Policy.Mode = "enforce"
			c.Policy.Rules = []config.Rule{{ID: "all", Match: config.Match{}, Decision: "deny"}}
			h := s.hook("pre", "pre")
			h.Env["FAKEGH_HOOK_DECISION"] = "deny"
			c.Hooks = []config.Hook{h}
			yes := true
			c.Logging.Enabled = &yes
		})
		s.env["GHOK_NESTED"] = "1"
		s.env["GHOK_DEPTH"] = "2"
		args := []string{"pr", "merge", "1"}
		result := s.gh(nil, args...)
		result.code(t, 0)
		r := s.onlyRecord(args, nil)
		if envMap(r.Env)["GHOK_DEPTH"] != "3" {
			t.Fatal("nested depth not incremented")
		}
		if _, err := os.Stat(filepath.Join(s.root, "pre.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("nested invocation ran hook")
		}
		records := s.logs()
		if len(records) != 1 || records[0].Decision != "passthrough-nested" {
			t.Fatalf("nested log=%+v", records)
		}
	})
	t.Run("shim-only-PATH", func(t *testing.T) {
		s := newSandbox(t)
		s.env["PATH"] = s.bin
		result := s.gh(nil, "version")
		result.code(t, 127)
		s.absentRecord()
	})
	t.Run("depth", func(t *testing.T) {
		s := newSandbox(t)
		s.env["GHOK_DEPTH"] = "999"
		result := s.gh(nil, "version")
		result.code(t, 1)
		s.absentRecord()
	})
	t.Run("probe-before-config-and-discovery", func(t *testing.T) {
		s := newSandbox(t)
		s.env["GHOK_PROBE"] = "1"
		s.env["PATH"] = s.bin
		if err := os.WriteFile(s.cfg, []byte("invalid: ["), 0600); err != nil {
			t.Fatal(err)
		}
		result := s.gh(nil, "anything")
		result.code(t, 0)
		if string(result.stdout) != "ghok-probe\n" || len(result.stderr) != 0 {
			t.Fatal("probe output changed")
		}
		s.absentRecord()
	})
	t.Run("invalid-config", func(t *testing.T) {
		s := newSandbox(t)
		if err := os.WriteFile(s.cfg, []byte("version: 1\nunknownField: true\n"), 0600); err != nil {
			t.Fatal(err)
		}
		result := s.gh([]byte{0, 255}, "version")
		result.code(t, 0)
		s.onlyRecord([]string{"version"}, []byte{0, 255})
		if bytes.Count(result.stderr, []byte("\n")) != 1 || !bytes.HasPrefix(result.stderr, []byte("ghok: invalid config")) {
			t.Fatalf("invalid config warning=%q", result.stderr)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid config wrote logs")
		}
	})
}

func (s *sandbox) logs() []logging.Record {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl"))
	if err != nil {
		s.t.Fatal(err)
	}
	var records []logging.Record
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var record logging.Record
		if err := json.Unmarshal(line, &record); err != nil {
			s.t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func TestLogsAndHookInputRedactSecrets(t *testing.T) {
	s := newSandbox(t)
	token := "ghp_" + strings.Repeat("s", 36)
	s.env["GH_TOKEN"] = token
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
		h := s.hook("post", "post")
		h.CaptureOutput = true
		h.Env["FAKEGH_HOOK_MESSAGE"] = token
		setBytes(h.Env, "FAKEGH_HOOK_STDERR_B64", []byte(token))
		c.Hooks = []config.Hook{h}
	})
	args := []string{"api", "user", "-H", "Authorization: token " + token}
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte(token))
	result := s.gh(nil, args...)
	result.code(t, 0)
	s.onlyRecord(args, nil)
	if string(result.stdout) != token {
		t.Fatal("redaction changed child execution bytes")
	}
	for _, path := range []string{filepath.Join(s.root, "post.json"), filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl")} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(token)) {
			t.Fatalf("secret leaked into %s", filepath.Base(path))
		}
	}
	input := s.hookInput("post")
	if !strings.Contains(strings.Join(input.Invocation.Argv, " "), "[REDACTED]") {
		t.Fatal("hook argv missing redaction marker")
	}
	if input.Result == nil || input.Result.Stdout == nil {
		t.Fatal("missing redacted capture")
	}
	captured, err := base64.StdEncoding.DecodeString(input.Result.Stdout.Base64)
	if err != nil || bytes.Contains(captured, []byte(token)) || !bytes.Contains(captured, []byte("[REDACTED]")) || input.Result.Stdout.Size != int64(len(token)) {
		t.Fatal("hook capture redaction changed metadata or leaked secret")
	}
	records := s.logs()
	if len(records) != 1 || records[0].InvocationID == "" || records[0].Exec == nil {
		t.Fatalf("log envelope=%+v", records)
	}
	if records[0].Exec.Stdout != nil || records[0].Exec.Stderr != nil {
		t.Fatal("logs retained captured output")
	}
}

func TestNoConfigDefaultsAndLoggingFailures(t *testing.T) {
	t.Run("no-config-logs", func(t *testing.T) {
		s := newSandbox(t)
		if err := os.Remove(s.cfg); err != nil {
			t.Fatal(err)
		}
		result := s.gh(nil, "version")
		result.code(t, 0)
		s.onlyRecord([]string{"version"}, nil)
		if len(result.stderr) != 0 || len(s.logs()) != 1 {
			t.Fatal("default logging behavior changed")
		}
	})
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("log-failure/debug=%v", debug), func(t *testing.T) {
			s := newSandbox(t)
			bad := filepath.Join(s.root, "not-directory")
			if err := os.WriteFile(bad, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				c.Logging.Path = filepath.Join(bad, "log")
			})
			if debug {
				s.env["GHOK_DEBUG"] = "1"
			}
			result := s.gh(nil, "version")
			result.code(t, 0)
			s.onlyRecord([]string{"version"}, nil)
			if (len(result.stderr) > 0) != debug {
				t.Fatalf("debug diagnostic=%q", result.stderr)
			}
		})
	}
}

func waitFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(path); err == nil && len(body) > 0 {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fixture readiness: %s", filepath.Base(path))
	return nil
}

func TestSignalDuringPreHookCancelsAndNeverStartsGh(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(strconv.Itoa(int(sig)), func(t *testing.T) {
			s := newSandbox(t)
			ready := filepath.Join(s.root, "hook-ready")
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				h := s.hook("pre", "pre")
				h.Env["FAKEGH_HOOK_SLEEP"] = "1m"
				h.Env["FAKEGH_HOOK_READY"] = ready
				c.Hooks = []config.Hook{h}
			})
			cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, "version")
			defer cancel()
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			pid, err := strconv.Atoi(string(waitFile(t, ready)))
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
			result.signaled(t, sig)
			s.absentRecord()
			s.hookInput("pre")
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("canceled hook remains alive: pid=%d err=%v", pid, err)
			}
		})
	}
}
