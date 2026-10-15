//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func decodeJSON(t *testing.T, data []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("invalid command JSON: %v; output=%q", err, data)
	}
}

func TestManagementVersionAndHelp(t *testing.T) {
	s := newSandbox(t)
	result := s.management(nil, "version")
	result.code(t, 0)
	for _, field := range []string{"ghok", "e2e-version", "e2e-commit", runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH} {
		if !bytes.Contains(result.stdout, []byte(field)) {
			t.Fatalf("version missing %q", field)
		}
	}
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"help", "normalize"}, {"hooks", "--help"}, {"config", "--help"}} {
		result := s.management(nil, args...)
		result.code(t, 0)
		if len(result.stdout) == 0 || len(result.stderr) != 0 {
			t.Fatalf("help output for %q", args)
		}
	}
	s.absentRecord()
}

func TestManagementUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"unknown-command"}, {"version", "unexpected"}, {"version", "--invalid"},
		{"normalize"}, {"normalize", "pr", "merge", "1"}, {"normalize", "--allow-api", "--no-resolve", "--", "pr", "merge", "1"},
		{"explain", "--allow-api", "--no-resolve", "--", "pr", "merge", "1"}, {"hooks", "unknown"},
		{"hooks", "test", "--phase", "bad", "--", "version"}, {"hooks", "test", "--"},
		{"hooks", "test", "--allow-api", "--no-resolve", "--", "version"},
		{"config", "unknown"}, {"doctor", "--unknown"}, {"shim", "install", "--bin-dir", ""},
		{"config", "schema"}, {"schema", "unknown"}, {"schema", "hook-input", "--project"}, {"schema", "hook-input", "extra"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			s := newSandbox(t)
			result := s.management(nil, args...)
			result.code(t, 2)
			if !bytes.HasPrefix(result.stderr, []byte("ghok:")) {
				t.Fatal("missing usage diagnostic")
			}
			s.absentRecord()
		})
	}
}

func TestManagementNormalize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		stdin  []byte
		level  model.Level
		action string
	}{
		{"native", []string{"pr", "merge", "7", "--admin", "-R", "acme/project"}, nil, model.LevelL1, "pr.merge.direct"},
		{"REST", []string{"api", "repos/acme/project/pulls/7/merge", "--method", "PUT"}, nil, model.LevelL2, "pr.merge.direct"},
		{"GraphQL-stdin", []string{"api", "graphql", "--input", "-"}, []byte(`{"query":"mutation { mergePullRequest(input:{pullRequestId:\"PR_node\"}) { clientMutationId } }"}`), model.LevelL3, "pr.merge.direct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			result := s.management(tc.stdin, append([]string{"normalize", "--json", "--"}, tc.args...)...)
			result.code(t, 0)
			var normalized model.Result
			decodeJSON(t, result.stdout, &normalized)
			if normalized.SchemaVersion != 1 || normalized.Classification.Level != tc.level || len(normalized.Operations) != 1 || normalized.Operations[0].Action != tc.action {
				t.Fatalf("normalized result=%+v", normalized)
			}
			s.absentRecord()
		})
	}
	t.Run("redacted", func(t *testing.T) {
		s := newSandbox(t)
		token := "ghp_" + strings.Repeat("n", 36)
		s.env["GH_TOKEN"] = token
		result := s.management(nil, "normalize", "--", "api", "user", "-H", "Authorization: token "+token)
		result.code(t, 0)
		if bytes.Contains(result.stdout, []byte(token)) || !bytes.Contains(result.stdout, []byte("[REDACTED]")) {
			t.Fatal("normalize redaction failed")
		}
		s.absentRecord()
	})
	t.Run("local-resolution-no-child", func(t *testing.T) {
		s := newSandbox(t)
		s.env["GH_REPO"] = "acme/project"
		result := s.management(nil, "normalize", "--", "pr", "merge", "7")
		result.code(t, 0)
		var normalized model.Result
		decodeJSON(t, result.stdout, &normalized)
		if len(normalized.Operations) != 1 || normalized.Operations[0].Target.Repository != "acme/project" {
			t.Fatalf("local target=%+v", normalized.Operations)
		}
		s.absentRecord()
	})
	t.Run("explicit-api-resolution", func(t *testing.T) {
		s := newSandbox(t)
		s.env["GH_HOST"] = "github.com"
		s.env["FAKEGH_API_RESPONSE"] = `{"data":{"node":{"__typename":"PullRequest","number":7,"repository":{"nameWithOwner":"acme/project"}}}}`
		result := s.management(nil, "normalize", "--allow-api", "--", "api", "graphql", "-f", `query=mutation { mergePullRequest(input:{pullRequestId:"PR_node"}) { clientMutationId } }`)
		result.code(t, 0)
		var normalized model.Result
		decodeJSON(t, result.stdout, &normalized)
		if len(normalized.Operations) != 1 || normalized.Operations[0].Target.Repository != "acme/project" {
			t.Fatalf("API target=%+v", normalized.Operations)
		}
		records := s.records()
		if len(records) != 1 || envMap(records[0].Env)["GHOK_NESTED"] != "1" {
			t.Fatalf("API invocation=%+v", records)
		}
	})
}

func TestManagementMergeDetails(t *testing.T) {
	s := newSandbox(t)
	for _, tc := range []struct {
		flag, action string
		detail       []string
	}{
		{"", "pr.merge.request", []string{"server", "direct merge, queue, or auto-merge"}},
		{"--admin", "pr.merge.direct", []string{"bypasses requirements", "merge queue", "server may still reject"}},
		{"--auto", "pr.merge.auto.enable", []string{"enable auto-merge", "not a completed merge"}},
		{"--disable-auto", "pr.merge.auto.disable", []string{"disable auto-merge", "not a direct merge"}},
	} {
		args := []string{"normalize", "--", "pr", "merge", "12", "-R", "acme/project"}
		if tc.flag != "" {
			args = append(args, tc.flag)
		}
		result := s.management(nil, args...)
		result.code(t, 0)
		var normalized model.Result
		decodeJSON(t, result.stdout, &normalized)
		op := normalized.Operations[0]
		if op.Action != tc.action {
			t.Fatalf("merge action=%s, want %s", op.Action, tc.action)
		}
		for _, meaning := range tc.detail {
			if !strings.Contains(op.Detail, meaning) {
				t.Fatalf("merge detail=%q, missing %q", op.Detail, meaning)
			}
		}
		if tc.flag != "" && strings.Contains(op.Detail, "state decide") {
			t.Fatalf("specific merge action has generic server-choice detail: %q", op.Detail)
		}
	}
	s.absentRecord()
}

func TestManagementLocalResolution(t *testing.T) {
	for _, source := range []string{"git", "env"} {
		t.Run(source, func(t *testing.T) {
			s := newSandbox(t)
			if source == "git" {
				if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(s.cwd, ".git", "config"), []byte("[remote \"origin\"]\nurl = https://ghe.example/acme/project.git\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				s.env["GH_REPO"], s.env["GH_HOST"] = "acme/project", "ghe.example"
			}
			s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("local", "pre")} })
			for _, flags := range [][]string{nil, {"--resolve"}, {"--resolve=false"}, {"--no-resolve"}} {
				args := append([]string{"normalize"}, flags...)
				result := s.management(nil, append(args, "--", "pr", "merge", "12")...)
				result.code(t, 0)
				var normalized model.Result
				decodeJSON(t, result.stdout, &normalized)
				op := normalized.Operations[0]
				if source == "git" && len(flags) != 0 && flags[0] == "--no-resolve" {
					if op.Target.Repository != "" || op.Target.Owner != "" || op.Target.Host != "" {
						t.Fatalf("disabled resolution target=%+v", op.Target)
					}
				} else if op.Target.Repository != "acme/project" || op.Target.Owner != "acme" || op.Target.Host != "ghe.example" {
					t.Fatalf("resolved target=%+v", op.Target)
				}
			}
			for _, command := range []string{"explain", "hooks"} {
				args := []string{command}
				if command == "hooks" {
					args = append(args, "test")
				}
				result := s.management(nil, append(args, "--", "pr", "merge", "12")...)
				result.code(t, 0)
				if command == "explain" {
					if !bytes.Contains(result.stdout, []byte(`"repository":"acme/project"`)) || bytes.Contains(result.stdout, []byte("Unresolved target.repository:")) {
						t.Fatalf("explain target=%s", result.stdout)
					}
				} else if input := s.hookInput("local"); input.Operations[0].Target.Repository != "acme/project" || input.Context == nil {
					t.Fatalf("hook target=%+v context=%+v", input.Operations, input.Context)
				}
				if source == "git" {
					result = s.management(nil, append(args, "--no-resolve", "--", "pr", "merge", "12")...)
					result.code(t, 0)
					if command == "explain" {
						if !bytes.Contains(result.stdout, []byte("Unresolved target.repository:")) {
							t.Fatalf("disabled explain resolution=%s", result.stdout)
						}
					} else if input := s.hookInput("local"); input.Operations[0].Target.Repository != "" || input.Context != nil {
						t.Fatalf("disabled hook resolution=%+v", input)
					}
				}
			}
			s.absentRecord()
		})
	}
}

func TestManagementExplain(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = "enforce"
		c.Policy.Rules = []config.Rule{{ID: "deny-merge", Match: config.Match{Operations: config.StringList{"pr.merge.*"}}, Decision: "deny", Reason: "test rule reason"}}
		pre, post := s.hook("pre-candidate", "pre"), s.hook("post-candidate", "post")
		pre.Match.Repositories = config.StringList{"acme/*"}
		c.Hooks = []config.Hook{pre, post}
	})
	result := s.management(nil, "explain", "--no-resolve", "--", "pr", "merge", "7")
	result.code(t, 0)
	// Decisions lead; operation diagnostics and policy reasons are not repeated.
	if !bytes.HasPrefix(result.stdout, []byte("Decision: deny (mode=enforce)\n")) {
		t.Fatalf("explain conclusion=%s", result.stdout)
	}
	for _, key := range []string{"L1", "pr.merge.", "target.repository", "deny-merge", "pre-candidate", "post-candidate", "maybe"} {
		if !bytes.Contains(result.stdout, []byte(key)) {
			t.Fatalf("explain omitted %q", key)
		}
	}
	for _, reason := range []string{"test rule reason", "Unresolved target.repository:", "Unresolved target.owner:", "Unresolved target.host:"} {
		if bytes.Count(result.stdout, []byte(reason)) != 1 {
			t.Fatalf("explain repeats or omits %q: %s", reason, result.stdout)
		}
	}
	operations, policy := bytes.Index(result.stdout, []byte("\nOperations:")), bytes.Index(result.stdout, []byte("\nPolicy:"))
	if operations < 0 || policy <= operations || bytes.Index(result.stdout, []byte("Hooks that WOULD run (post)")) >= operations {
		t.Fatalf("explain section ordering=%s", result.stdout)
	}
	s.absentRecord()
	for _, id := range []string{"pre-candidate", "post-candidate"} {
		if _, err := os.Stat(filepath.Join(s.root, id+".json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("explain executed hook")
		}
	}
	token := "ghp_" + strings.Repeat("x", 36)
	s.env["GH_TOKEN"] = token
	for _, secrets := range []bool{false, true} {
		args := []string{"explain"}
		if secrets {
			args = append(args, "--show-secrets")
		}
		args = append(args, "--", "api", "user", "-H", "Authorization: token "+token)
		result := s.management(nil, args...)
		result.code(t, 0)
		if !bytes.Contains(result.stdout, []byte("Matched rules: none")) || bytes.Contains(result.stdout, []byte("Deny reason:")) {
			t.Fatal("explain reports incorrect rule matches or deny reasons")
		}
		if bytes.Contains(result.stdout, []byte(token)) != secrets {
			t.Fatalf("explain secret visibility=%v", secrets)
		}
	}
}

func TestManagementExplainDecisions(t *testing.T) {
	for _, tc := range []struct {
		mode, rule, decision string
	}{{"observe", "", "allow"}, {"observe", "deny", "audit"}, {"enforce", "audit", "audit"}, {"enforce", "deny", "deny"}} {
		t.Run(tc.mode+"-"+tc.rule, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = tc.mode
				if tc.rule != "" {
					c.Policy.Rules = []config.Rule{{ID: "rule", Decision: tc.rule, Reason: "consumer reason"}}
				}
			})
			result := s.management(nil, "explain", "--", "pr", "merge", "12", "-R", "acme/project")
			result.code(t, 0)
			if !bytes.HasPrefix(result.stdout, []byte("Decision: "+tc.decision+" (mode="+tc.mode+")\n")) {
				t.Fatalf("explain decision=%s", result.stdout)
			}
			if tc.rule != "" && bytes.Count(result.stdout, []byte("consumer reason")) != 1 {
				t.Fatalf("explain reason duplication=%s", result.stdout)
			}
			s.absentRecord()
		})
	}
}

func TestManagementHooksListAndTest(t *testing.T) {
	for _, mode := range []string{"enforce", "observe"} {
		t.Run(mode, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = mode
				pre, post := s.hook("pre", "pre"), s.hook("post", "post")
				pre.Env["FAKEGH_HOOK_DECISION"] = "deny"
				pre.Env["FAKEGH_HOOK_MESSAGE"] = "test denial"
				c.Hooks = []config.Hook{pre, post}
			})
			result := s.management(nil, "hooks", "list", "--json")
			result.code(t, 0)
			var list []struct {
				ID    string `json:"id"`
				Phase string `json:"phase"`
			}
			decodeJSON(t, result.stdout, &list)
			if len(list) != 2 || list[0].ID != "pre" || list[1].ID != "post" || list[0].Phase != "pre" {
				t.Fatalf("hooks list=%+v", list)
			}
			result = s.management(nil, "hooks", "list")
			result.code(t, 0)
			if !bytes.Contains(result.stdout, []byte("pre")) || !bytes.Contains(result.stdout, []byte("post")) {
				t.Fatal("human hook list omitted IDs")
			}
			for _, phase := range []string{"pre", "post"} {
				result = s.management(nil, "hooks", "test", "--json", "--phase", phase, "--", "pr", "merge", "7", "-R", "acme/project")
				want := 0
				if phase == "pre" && mode == "enforce" {
					want = 1
				}
				result.code(t, want)
				var report struct {
					Phase     string                `json:"phase"`
					Synthetic bool                  `json:"synthetic"`
					Results   []protocol.HookResult `json:"results"`
					Final     struct {
						Decision  string `json:"decision"`
						WouldDeny bool   `json:"wouldDeny"`
					} `json:"final"`
				}
				decodeJSON(t, result.stdout, &report)
				if report.Phase != phase || !report.Synthetic || len(report.Results) != 1 || report.Results[0].HookID != phase || report.Results[0].Outcome != "ok" {
					t.Fatalf("hook test report=%+v", report)
				}
				if phase == "pre" {
					if !report.Final.WouldDeny || (report.Final.Decision == "deny") != (mode == "enforce") {
						t.Fatalf("final=%+v", report.Final)
					}
				}
				input := s.hookInput(phase)
				if phase == "post" && (input.Result == nil || input.Result.ExitCode != 0) {
					t.Fatal("synthetic post hook missing execution result")
				}
			}
			result = s.management(nil, "hooks", "test", "--phase", "post", "--", "version")
			result.code(t, 0)
			if !bytes.Contains(result.stdout, []byte("post")) {
				t.Fatal("human hook test omitted result")
			}
			s.absentRecord()
		})
	}
	t.Run("hook-error-status", func(t *testing.T) {
		s := newSandbox(t)
		s.configure(func(c *config.Config) {
			h := s.hook("pre", "pre")
			h.Env["FAKEGH_HOOK_EXIT"] = "19"
			c.Hooks = []config.Hook{h}
		})
		result := s.management(nil, "hooks", "test", "--json", "--", "version")
		result.code(t, 1)
		s.absentRecord()
	})
}

func TestManagementHookSources(t *testing.T) {
	s := newSandbox(t)
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(s.cwd, ".ghok.yaml")
	command, err := json.Marshal([]string{fakeBinary})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte("version: 1\nhooks: [{id: project, phase: post, command: "+string(command)+"}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	s.configure(func(c *config.Config) {
		c.Hooks = []config.Hook{s.hook("user", "pre")}
	})
	s.management(nil, "config", "trust").code(t, 0)
	result := s.management(nil, "hooks", "list", "--json")
	result.code(t, 0)
	var list []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}
	decodeJSON(t, result.stdout, &list)
	if len(list) != 2 || list[0].ID != "user" || list[0].Source != s.cfg || list[1].ID != "project" || list[1].Source != project {
		t.Fatalf("hook provenance=%+v", list)
	}
	result = s.management(nil, "hooks", "list")
	result.code(t, 0)
	for _, source := range []string{s.cfg, project} {
		if !bytes.Contains(result.stdout, []byte("source: "+source)) {
			t.Fatalf("human hook provenance=%s", result.stdout)
		}
	}
	s.absentRecord()
}

func TestManagementConfigAndDoctor(t *testing.T) {
	s := newSandbox(t)
	result := s.management(nil, "config", "path")
	result.code(t, 0)
	if strings.TrimSpace(string(result.stdout)) != s.cfg {
		t.Fatalf("config path=%q", result.stdout)
	}
	result = s.management(nil, "config", "validate")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte(s.cfg)) {
		t.Fatal("validation omitted config path")
	}
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
	result = s.management(nil, "doctor", "--json")
	result.code(t, 0)
	var checks []install.Check
	decodeJSON(t, result.stdout, &checks)
	byName := make(map[string]install.Check)
	for _, check := range checks {
		byName[check.Name] = check
	}
	for _, name := range []string{"ghok", "real-gh", "path-precedence", "shim-ownership", "recursion-risk", "gh-version", "platform-process", "config", "log-writable"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("doctor missing %s", name)
		}
	}
	if byName["config"].Status != install.StatusOK || byName["real-gh"].Status != install.StatusOK {
		t.Fatal("healthy doctor check failed")
	}
	records := s.records()
	if len(records) != 1 || len(records[0].Argv) != 1 || records[0].Argv[0] != "--version" {
		t.Fatalf("doctor child=%+v", records)
	}
	result = s.management(nil, "doctor")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("real-gh")) || !bytes.Contains(result.stdout, []byte("config")) {
		t.Fatal("human doctor omitted checks")
	}
	if err := os.WriteFile(s.cfg, []byte("version: 1\ninvalid: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result = s.management(nil, "config", "validate")
	result.code(t, 1)
	result = s.management(nil, "doctor", "--json")
	result.code(t, 1)
	checks = nil
	decodeJSON(t, result.stdout, &checks)
	found := false
	for _, check := range checks {
		if check.Name == "config" && check.Status == install.StatusFail {
			found = true
		}
	}
	if !found {
		t.Fatal("doctor did not report invalid configuration")
	}
}

func TestManagementDoctorRecordedCustomInstall(t *testing.T) {
	s := newSandbox(t)
	s.management(nil, "shim", "install", "--bin-dir", s.bin).code(t, 0)
	bin, err := filepath.EvalSymlinks(s.bin)
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(bin, "gh")
	defaultShim := filepath.Join(s.env["HOME"], ".local", "bin", "gh")
	if _, err := os.Lstat(defaultShim); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected default shim: %v", err)
	}
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
	result := s.management(nil, "doctor")
	result.code(t, 0)
	for _, diagnostic := range []string{"ok path-precedence: " + shim + " is the first executable gh", "ok shim-ownership: " + shim + " is an owned symlink"} {
		if !bytes.Contains(result.stdout, []byte(diagnostic)) {
			t.Fatalf("doctor omitted custom shim diagnostic %q: %s", diagnostic, result.stdout)
		}
	}
	if bytes.Contains(result.stdout, []byte(defaultShim)) {
		t.Fatalf("doctor inspected absent default shim: %s", result.stdout)
	}
	result = s.management(nil, "doctor", "--json")
	result.code(t, 0)
	var checks []install.Check
	decodeJSON(t, result.stdout, &checks)
	found := 0
	for _, check := range checks {
		if check.Name == "path-precedence" || check.Name == "shim-ownership" {
			found++
			if check.Status != install.StatusOK || !strings.HasPrefix(check.Detail, shim+" ") {
				t.Fatalf("custom shim check=%+v", check)
			}
		}
	}
	if found != 2 {
		t.Fatalf("expected one pair of custom shim checks: %+v", checks)
	}
}

func TestManagementDoctorHookFailures(t *testing.T) {
	for _, kind := range []string{"missing", "not-executable", "writable"} {
		t.Run(kind, func(t *testing.T) {
			s := newSandbox(t)
			path := filepath.Join(s.root, "hook")
			if kind != "missing" {
				mode := os.FileMode(0600)
				if kind == "writable" {
					mode = 0777
				}
				if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			s.configure(func(c *config.Config) { c.Hooks = []config.Hook{{ID: "bad", Phase: "pre", Command: []string{path}}} })
			result := s.management(nil, "doctor", "--json")
			result.code(t, 1)
			var checks []install.Check
			decodeJSON(t, result.stdout, &checks)
			found := false
			for _, check := range checks {
				if strings.Contains(check.Name, "bad") && check.Status == install.StatusFail {
					found = true
				}
			}
			if !found {
				t.Fatalf("doctor omitted hook failure: %+v", checks)
			}
		})
	}
}

func TestManagementEnableDisableOwnership(t *testing.T) {
	s := newSandbox(t)
	result := s.management(nil, "shim", "install", "--bin-dir", s.bin)
	result.code(t, 0)
	for _, value := range []string{filepath.Join(s.bin, "gh"), s.real, "PATH"} {
		if !bytes.Contains(result.stdout, []byte(value)) {
			t.Fatalf("enable output omitted %q", value)
		}
	}
	s.absentRecord()
	for _, name := range []string{".bashrc", ".zshrc", ".profile"} {
		if _, err := os.Stat(filepath.Join(s.env["HOME"], name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("enable modified shell configuration")
		}
	}
	result = s.management(nil, "shim", "uninstall", "--bin-dir", s.bin)
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte(filepath.Join(s.bin, "gh"))) {
		t.Fatal("disable omitted shim path")
	}
	if _, err := os.Lstat(filepath.Join(s.bin, "gh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disable left shim")
	}
	if _, err := os.Stat(s.real); err != nil {
		t.Fatal("disable removed real gh")
	}
	result = s.management(nil, "shim", "uninstall", "--bin-dir", s.bin)
	result.code(t, 0)
	if err := os.WriteFile(filepath.Join(s.bin, "gh"), []byte("foreign file"), 0700); err != nil {
		t.Fatal(err)
	}
	result = s.management(nil, "shim", "install", "--bin-dir", s.bin)
	result.code(t, 1)
	result = s.management(nil, "shim", "uninstall", "--bin-dir", s.bin)
	result.code(t, 0)
	body, err := os.ReadFile(filepath.Join(s.bin, "gh"))
	if err != nil || string(body) != "foreign file" {
		t.Fatal("management replaced foreign file")
	}
}

func TestManagementConfigValidateSelectedFileAndNotes(t *testing.T) {
	s := newSandbox(t)
	selected := filepath.Join(s.cwd, "selected.yaml")
	body := []byte("version: 1\npolicy:\n  mode: invalid\n  unknownMutation: invalid\nhooks:\n  - id: bad\n    phase: invalid\n    command: []\nruntime:\n  stdinMaxBytes: -1\n")
	if err := os.WriteFile(selected, body, 0600); err != nil {
		t.Fatal(err)
	}
	result := s.management(nil, "config", "validate", "--file", "selected.yaml")
	result.code(t, 1)
	for _, field := range []string{"policy.mode", "policy.unknownMutation", "hooks[0].phase", "hooks[0].command", "runtime.stdinMaxBytes"} {
		if !bytes.Contains(result.stderr, []byte(field)) {
			t.Fatalf("validation omitted independent error %q: %s", field, result.stderr)
		}
	}
	// --file does not replace or invalidate the global configuration.
	s.management(nil, "config", "validate").code(t, 0)
	if err := os.WriteFile(selected, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(selected, 0666); err != nil {
		t.Fatal(err)
	}
	result = s.management(nil, "config", "validate", "--file", selected)
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte(selected)) || !bytes.Contains(result.stdout, []byte("group/world-writable")) {
		t.Fatalf("validation omitted selected file/permissions note: %s", result.stdout)
	}
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(s.cwd, ".ghok.yaml")
	if err := os.WriteFile(project, []byte("version: 1\nhooks: [{id: project, phase: pre, command: [./missing-hook]}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result = s.management(nil, "config", "validate")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte(".ghok.yaml")) {
		t.Fatal("validation omitted untrusted project note")
	}
	s.absentRecord()
}

func TestManagementCobraUsageBoundaries(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		group bool
	}{
		{[]string{"shim"}, true},
		{[]string{"hooks"}, true},
		{[]string{"config"}, true},
		{[]string{"--help", "extra"}, false},
		{[]string{"doctor", "--json=bad"}, false},
		{[]string{"config", "validate", "--file"}, false},
		{[]string{"config", "trust", "one", "two"}, false},
		{[]string{"config", "untrust", "one", "two"}, false},
		{[]string{"help", "unknown-command"}, false},
		{[]string{"completion"}, false},
		{[]string{"normalize", "version", "--", "version"}, false},
		{[]string{"explain", "version", "--", "version"}, false},
		{[]string{"hooks", "test", "version", "--", "version"}, false},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			s := newSandbox(t)
			result := s.management(nil, tc.args...)
			result.code(t, 2)
			if tc.group {
				if !bytes.Contains(result.stdout, []byte("Usage:")) || len(result.stderr) != 0 {
					t.Fatalf("bare group must show usage on stdout: %+v", result)
				}
			} else if len(result.stdout) != 0 || !bytes.HasPrefix(result.stderr, []byte("ghok:")) {
				t.Fatalf("usage error must report only on stderr: %+v", result)
			}
			s.absentRecord()
		})
	}
}

func TestManagementCobraPassthrough(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Hooks = []config.Hook{s.hook("passthrough", "pre")}
	})
	// These management-looking options, the second separator, and the empty
	// argument belong exclusively to gh; none may affect management parsing.
	args := []string{"api", "user", "--json=false", "--resolve", "--allow-api", "--show-secrets", "--phase=post", "--help", "", "--", "-h", "help", "version"}
	result := s.management(nil, append([]string{"normalize", "--"}, args...)...)
	result.code(t, 0)
	var normalized model.Result
	decodeJSON(t, result.stdout, &normalized)
	if !reflect.DeepEqual(normalized.Source.Argv, args) {
		t.Fatalf("normalize changed passthrough argv: %q", normalized.Source.Argv)
	}
	result = s.management(nil, append([]string{"explain", "--"}, args...)...)
	result.code(t, 0)
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.stdout, encoded) || bytes.Contains(result.stdout, []byte("WARNING: --show-secrets")) {
		t.Fatalf("explain consumed passthrough options: %s", result.stdout)
	}
	result = s.management(nil, append([]string{"hooks", "test", "--json", "--"}, args...)...)
	result.code(t, 0)
	var report struct {
		Phase string `json:"phase"`
	}
	decodeJSON(t, result.stdout, &report)
	input := s.hookInput("passthrough")
	if report.Phase != "pre" || !reflect.DeepEqual(input.Invocation.Argv, args) {
		t.Fatalf("hooks test consumed passthrough options: phase=%s argv=%q", report.Phase, input.Invocation.Argv)
	}
	s.absentRecord()
}

func TestManagementConfigInit(t *testing.T) {
	s := newSandbox(t)
	s.management(nil, "config", "init").code(t, 1) // not inside a git repository
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	result := s.management(nil, "config", "init")
	result.code(t, 0)
	project := filepath.Join(s.cwd, ".ghok.yaml")
	data, err := os.ReadFile(project)
	if err != nil || !bytes.HasPrefix(data, []byte("# yaml-language-server: $schema=")) || !bytes.Contains(data, []byte("project.schema.json")) {
		t.Fatalf("project config: err=%v\n%s", err, data)
	}
	s.management(nil, "config", "init").code(t, 1) // never overwrites
	if err := os.Remove(s.cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	s.management(nil, "config", "init", "--scope", "user").code(t, 0)
	if data, err := os.ReadFile(s.cfg); err != nil || !bytes.Contains(data, []byte("config.schema.json")) {
		t.Fatalf("user config: err=%v", err)
	}
	s.management(nil, "config", "validate").code(t, 0)
	s.management(nil, "config", "init", "--scope", "team").code(t, 1)
}
