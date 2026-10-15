//go:build unix

package e2e

import (
	"bytes"
	"crypto/sha256"
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
		if logs := s.logs(); len(logs) != 1 || logs[0].Decision != "passthrough-invalid-config" || len(logs[0].Diagnostics) == 0 {
			t.Fatalf("invalid config fallback log=%+v", logs)
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
		h.RedactInput = true
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

func TestLogsMaskContentWithoutChangingHookInput(t *testing.T) {
	token := "ghp_" + strings.Repeat("p", 36)
	comment := "private GraphQL comment " + token
	query := `mutation { addComment(input: {subjectId: "ISSUE_private_node", body: "` + comment + `"}) { clientMutationId } }`
	for _, tc := range []struct {
		name, content, key, action, node string
		args                             []string
	}{
		{name: "issue-body", content: "privatebody", action: "issue.edit", args: []string{"issue", "edit", "7", "-R", "acme/project", "--body", "privatebody"}},
		{name: "pr-title", content: "A private PR title", key: "title", action: "pr.edit", args: []string{"pr", "edit", "7", "-R", "acme/project", "--title", "A private PR title"}},
		{name: "rest-body", content: "private REST body " + token, key: "body", action: "comment.create", args: []string{"api", "repos/acme/project/issues/7/comments", "-f", "body=private REST body " + token}},
		{name: "graphql-comment", content: comment, key: "body", action: "comment.create", node: "ISSUE_private_node", args: []string{"api", "graphql", "-f", "query=" + query}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.env["GH_TOKEN"] = token
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				h := s.hook("pre", "pre")
				h.RedactInput = true
				c.Hooks = []config.Hook{h}
			})
			result := s.gh(nil, tc.args...)
			result.code(t, 0)
			s.onlyRecord(tc.args, nil)
			input := s.hookInput("pre")
			if len(input.Operations) != 1 || input.Operations[0].Action != tc.action {
				t.Fatalf("hook operation=%+v", input.Operations)
			}
			wantContent := strings.ReplaceAll(tc.content, token, "[REDACTED]")
			if !strings.Contains(strings.Join(input.Invocation.Argv, "\n"), wantContent) {
				t.Fatalf("trusted hook lost ordinary content: %q", input.Invocation.Argv)
			}
			if tc.key != "" && input.Operations[0].Parameters[tc.key] != wantContent {
				t.Fatalf("trusted hook parameter=%v, want=%q", input.Operations[0].Parameters[tc.key], wantContent)
			}
			hookBytes, err := os.ReadFile(filepath.Join(s.root, "pre.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(hookBytes, []byte(token)) {
				t.Fatal("trusted hook received unredacted credential")
			}
			logBytes, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			ordinaryContent := strings.TrimSpace(strings.TrimSuffix(tc.content, token))
			if bytes.Contains(logBytes, []byte(ordinaryContent)) || bytes.Contains(logBytes, []byte(token)) || !bytes.Contains(logBytes, []byte("[content: ")) {
				t.Fatalf("log content masking failed: %s", logBytes)
			}
			records := s.logs()
			if len(records) != 1 || len(records[0].Operations) != 1 || records[0].Operations[0].Action != tc.action || records[0].Source == nil {
				t.Fatalf("log lost operation/source: %+v", records)
			}
			op := records[0].Operations[0]
			if tc.node != "" {
				if op.Target.NodeID != tc.node {
					t.Fatalf("log node target=%+v", op.Target)
				}
				fingerprint := records[0].Source.GraphQL
				prefix := fmt.Sprintf("[graphql document: %d bytes, sha256:", len(query))
				if !strings.HasPrefix(fingerprint, prefix) || !strings.HasSuffix(fingerprint, "]") {
					t.Fatalf("GraphQL source is not a fingerprint: %q", fingerprint)
				}
				hashPrefix := strings.TrimSuffix(strings.TrimPrefix(fingerprint, prefix), "]")
				sum := sha256.Sum256([]byte(query))
				if len(hashPrefix) < 8 || !strings.HasPrefix(fmt.Sprintf("%x", sum), hashPrefix) {
					t.Fatalf("GraphQL fingerprint does not identify original document: %q", fingerprint)
				}
				if bytes.Contains(logBytes, []byte("addComment(input:")) {
					t.Fatal("log retained GraphQL document")
				}
			} else if op.Target.Repository != "acme/project" || op.Target.Number == nil || *op.Target.Number != 7 {
				t.Fatalf("log repository/number target=%+v", op.Target)
			}
		})
	}
}

func TestRoutineConfigNotesAbsentFromLogs(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
	})
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cwd, ".ghok.yaml"), []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := s.gh(nil, "version")
	result.code(t, 0)
	s.onlyRecord([]string{"version"}, nil)
	if len(result.stderr) != 0 {
		t.Fatalf("routine configuration emitted diagnostic: %s", result.stderr)
	}
	records := s.logs()
	if len(records) != 1 {
		t.Fatalf("logs=%+v", records)
	}
	for _, diagnostic := range records[0].Diagnostics {
		if strings.Contains(diagnostic, "Global config:") || strings.Contains(diagnostic, "Project config:") {
			t.Fatalf("routine configuration leaked into diagnostics: %q", diagnostic)
		}
	}
}

func TestLoggedGraphQLStdinAnalysisEligibility(t *testing.T) {
	query := `mutation { addComment(input: {subjectId: "ISSUE_private_node", body: "private comment"}) { clientMutationId } }`
	payload, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	for _, enforce := range []bool{false, true} {
		t.Run(fmt.Sprintf("enforce=%v", enforce), func(t *testing.T) {
			s := newSandbox(t)
			if enforce {
				s.configure(func(c *config.Config) {
					yes := true
					c.Logging.Enabled = &yes
					c.Policy.Mode = config.ModeEnforce
					c.Policy.Rules = []config.Rule{{ID: "no-comments", Decision: "deny", Match: config.Match{Operations: config.StringList{"comment.create"}}}}
				})
			} else if err := os.Remove(s.cfg); err != nil {
				t.Fatal(err)
			}
			args := []string{"api", "graphql", "--input", "-"}
			result := s.gh(payload, args...)
			if enforce {
				result.code(t, 1)
				s.absentRecord()
			} else {
				result.code(t, 0)
				s.onlyRecord(args, payload)
			}
			records := s.logs()
			if len(records) != 1 || len(records[0].Operations) != 1 {
				t.Fatalf("logs=%+v", records)
			}
			record := records[0]
			notAnalyzed := false
			for _, diagnostic := range record.Diagnostics {
				notAnalyzed = notAnalyzed || diagnostic == "stdin not analyzed (nothing could act on it)"
			}
			if enforce {
				if record.Decision != "deny" || record.Operations[0].Action != "comment.create" || notAnalyzed {
					t.Fatalf("enforcing stdin analysis=%+v", record)
				}
			} else if record.Decision != "allow" || record.Operations[0].Family != "unknown" || record.Classification.MutationFree || !notAnalyzed {
				t.Fatalf("unneeded stdin analysis=%+v", record)
			}
		})
	}
}

func TestInvalidConfigWarningThrottleAndLogging(t *testing.T) {
	s := newSandbox(t)
	input := []byte{0, 'a', '\n', 255}
	args := []string{"api", "graphql", "--input", "-", "-f", "body=unchanged content"}
	writeInvalid := func(body string) {
		t.Helper()
		if err := os.WriteFile(s.cfg, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	invoke := func(warn bool) {
		t.Helper()
		if err := os.Remove(s.record); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		result := s.gh(input, args...)
		result.code(t, 0)
		s.onlyRecord(args, input)
		if warn {
			if bytes.Count(result.stderr, []byte("\n")) != 1 || !bytes.HasPrefix(result.stderr, []byte("ghok: invalid config")) {
				t.Fatalf("invalid config warning=%q", result.stderr)
			}
		} else if len(result.stderr) != 0 {
			t.Fatalf("warning was not throttled: %q", result.stderr)
		}
		count++
		records := s.logs()
		if len(records) != count {
			t.Fatalf("invalid config events=%d, want=%d", len(records), count)
		}
		record := records[count-1]
		if record.Decision != "passthrough-invalid-config" || !strings.Contains(strings.Join(record.Diagnostics, "\n"), "unknownField") {
			t.Fatalf("invalid config event lacks error diagnostics: %+v", record)
		}
	}
	writeInvalid("version: 1\nunknownField: true\n")
	invoke(true)
	invoke(false)
	stateDir := filepath.Join(filepath.Dir(s.cfg), "state")
	warnings, err := filepath.Glob(filepath.Join(stateDir, "invalid-config-*.warning"))
	if err != nil || len(warnings) != 1 {
		t.Fatalf("warning state=%v, err=%v", warnings, err)
	}
	for path, want := range map[string]os.FileMode{stateDir: 0700, warnings[0]: 0600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%#o, want=%#o", path, info.Mode().Perm(), want)
		}
	}
	timestamp, err := os.ReadFile(warnings[0])
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(timestamp)))
	if err != nil || time.Since(stamp) > time.Minute {
		t.Fatalf("warning timestamp=%q, err=%v", timestamp, err)
	}
	old := time.Now().Add(-11*time.Minute).UTC().Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(warnings[0], []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(true)
	invoke(false)
	writeInvalid("version: 1\nunknownFieldChanged: true\n")
	invoke(true)
	invoke(false)
	changed, err := filepath.Glob(filepath.Join(stateDir, "invalid-config-*.warning"))
	if err != nil || len(changed) != 2 {
		t.Fatalf("changed-error warning state=%v, err=%v", changed, err)
	}
	// Returning to the original error reuses that pair's independent throttle.
	writeInvalid("version: 1\nunknownField: true\n")
	invoke(false)
	// A state I/O failure must never hide the warning or prevent execution.
	if err := os.Remove(warnings[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(warnings[0], 0700); err != nil {
		t.Fatal(err)
	}
	invoke(true)
	invoke(true)
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
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
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
			t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
			result.signaled(t, sig)
			s.absentRecord()
			s.hookInput("pre")
			waitProcessGone(t, pid)
		})
	}
}

func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		} else if err != nil {
			t.Fatalf("checking helper pid=%d: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper remains alive after cleanup: pid=%d", pid)
}

func TestSignalDuringPostHookKeepsChildStatusAndReapsHook(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		for _, exit := range []int{0, 42} {
			t.Run(fmt.Sprintf("signal=%d/exit=%d", sig, exit), func(t *testing.T) {
				s := newSandbox(t)
				ready := filepath.Join(s.root, "hook-ready")
				s.configure(func(c *config.Config) {
					yes := true
					c.Logging.Enabled = &yes
					c.Hooks = []config.Hook{{
						ID: "post", Phase: config.PhasePost, Timeout: config.Duration(time.Minute),
						Command: []string{"/bin/sh", "-c", `printf '%s' "$$" > "$1"; exec /bin/sleep 30`, "hook", ready},
					}}
				})
				s.env["FAKEGH_EXIT"] = strconv.Itoa(exit)
				args := []string{"pr", "view", "1"}
				cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, args...)
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
				t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
				result.code(t, exit)
				waitProcessGone(t, pid)
				s.onlyRecord(args, nil)
				logs := s.logs()
				if len(logs) != 1 || logs[0].Exec == nil || logs[0].Exec.ExitCode != exit ||
					len(logs[0].Hooks) != 1 || logs[0].Hooks[0].Outcome != protocol.OutcomeCanceled ||
					!strings.Contains(strings.Join(logs[0].Diagnostics, "; "), "post-hooks interrupted by SIG") {
					t.Fatalf("post interruption log=%+v", logs)
				}
			})
		}
	}
}

func TestSignalDuringResolverReapsHelperAndNeverStartsUserGh(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(strconv.Itoa(int(sig)), func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				c.Policy.ProtectedRepositories = []string{"acme/*"}
				c.Runtime.ResolveTimeout = config.Duration(20 * time.Second)
			})
			s.env["GH_HOST"] = "github.com"
			ready := filepath.Join(s.root, "resolver-ready")
			s.env["FAKEGH_READY"], s.env["FAKEGH_SLEEP"] = ready, "1m"
			args := []string{"api", "graphql", "-f", `query=mutation { mergePullRequest(input:{pullRequestId:"PR_test_node"}) { clientMutationId } }`}
			cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, args...)
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
			t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
			result.signaled(t, sig)
			waitProcessGone(t, pid)
			records := s.records()
			if len(records) != 1 || envMap(records[0].Env)["GHOK_NESTED"] != "1" {
				t.Fatalf("user gh ran after interrupted resolution: %+v", records)
			}
		})
	}
}

func TestHelperPhasesPreserveIgnoredStartupSignals(t *testing.T) {
	for _, phase := range []string{config.PhasePre, config.PhasePost} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP} {
			t.Run(fmt.Sprintf("%s/signal=%d", phase, sig), func(t *testing.T) {
				s := newSandbox(t)
				ready, fifo := filepath.Join(s.root, "hook-ready"), filepath.Join(s.root, "release")
				if err := syscall.Mkfifo(fifo, 0600); err != nil {
					t.Fatal(err)
				}
				release, err := os.OpenFile(fifo, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = release.Close() }()
				s.configure(func(c *config.Config) {
					yes := true
					c.Logging.Enabled = &yes
					c.Hooks = []config.Hook{{
						ID: phase, Phase: phase,
						Command: []string{"/bin/sh", "-c", `printf '%s' "$$" > "$1"; read -r line < "$2"; printf '{"decision":"abstain"}'`, "hook", ready, fifo},
					}}
				})
				cmd, cancel := s.command("/bin/sh", nil, "-c", `trap '' INT HUP; exec "$@"`, "ignored", filepath.Join(s.bin, "gh"), "version")
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
				t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				if _, err := release.Write([]byte("continue\n")); err != nil {
					t.Fatal(err)
				}
				result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
				result.code(t, 0)
				waitProcessGone(t, pid)
				s.onlyRecord([]string{"version"}, nil)
				logs := s.logs()
				if len(logs) != 1 || len(logs[0].Hooks) != 1 || logs[0].Hooks[0].Outcome != protocol.OutcomeOK {
					t.Fatalf("ignored signal interrupted helper: %+v", logs)
				}
			})
		}
	}
}

// Post hooks can be limited to gh's outcome with match.result / match.exitCode;
// non-matching ones are not run at all.
func TestPostHooksMatchOutcome(t *testing.T) {
	for _, tc := range []struct {
		exit    string
		code    int
		ran     []string
		skipped []string
	}{
		{"0", 0, []string{"on-success", "on-zero"}, []string{"on-failure", "on-42"}},
		{"42", 42, []string{"on-failure", "on-42"}, []string{"on-success", "on-zero"}},
		{"1", 1, []string{"on-failure"}, []string{"on-success", "on-zero", "on-42"}},
	} {
		t.Run("exit"+tc.exit, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				success, failure, zero, fortyTwo := s.hook("on-success", "post"), s.hook("on-failure", "post"), s.hook("on-zero", "post"), s.hook("on-42", "post")
				success.Match.Results = config.StringList{config.ResultSuccess}
				failure.Match.Results = config.StringList{config.ResultFailure}
				zero.Match.ExitCodes = []int{0}
				fortyTwo.Match.ExitCodes = []int{42}
				c.Hooks = []config.Hook{success, failure, zero, fortyTwo}
			})
			s.env["FAKEGH_EXIT"] = tc.exit
			s.gh(nil, "version").code(t, tc.code)
			for _, id := range tc.ran {
				if in := s.hookInput(id); in.Result == nil || in.Result.ExitCode != tc.code {
					t.Fatalf("%s: result=%+v", id, in.Result)
				}
			}
			for _, id := range tc.skipped {
				if _, err := os.Stat(filepath.Join(s.root, id+".json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s ran for exit %s", id, tc.exit)
				}
			}
		})
	}
}

// match.token distinguishes GitHub App installation tokens (gain, Actions)
// from user credentials, e.g. to forbid PR writes authored by an App.
func TestRuleMatchesTokenKind(t *testing.T) {
	fake := "0123456789abcdefghijklmnopqrstuvwxyzAB" // not a real credential
	for _, tc := range []struct {
		token string
		code  int
	}{
		{"ghs" + "_" + fake, 1},
		{"ghp" + "_" + fake, 0},
		{"", 0},
	} {
		t.Run(tc.token[:min(4, len(tc.token))], func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				c.Policy.Rules = []config.Rule{{ID: "no-app-pr-writes", Decision: "deny", Reason: "PR writes must be authored by a user",
					Match: config.Match{Tokens: config.StringList{"app"}, Operations: config.StringList{"pr.*"}, Effects: config.StringList{"write"}}}}
				h := s.hook("pre", "pre")
				c.Hooks = []config.Hook{h}
			})
			if tc.token != "" {
				s.env["GH_TOKEN"] = tc.token
			}
			result := s.gh(nil, "pr", "close", "1", "-R", "o/r")
			result.code(t, tc.code)
			if tc.code == 1 {
				s.absentRecord()
				return
			}
			if in := s.hookInput("pre"); in.Invocation.TokenKind == "" || strings.Contains(string(mustJSON(t, in)), fake) {
				t.Fatalf("tokenKind=%q, token leaked into hook input", in.Invocation.TokenKind)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
