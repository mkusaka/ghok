//go:build unix

package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestMiscEnvironmentRule(t *testing.T) {
	for _, worker := range []string{"1", "0", ""} {
		t.Run("worker="+worker, func(t *testing.T) {
			s := newSandbox(t)
			s.env["MALL_GH_WORKER"] = worker
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				c.Policy.Rules = []config.Rule{{ID: "no-worker-merges", Decision: "deny", Reason: "worker merges disabled",
					Match: config.Match{Env: map[string]string{"MALL_GH_WORKER": "1"}, Operations: config.StringList{"pr.merge.*"}}}}
			})
			args := []string{"pr", "merge", "7", "--admin", "-R", "acme/project"}
			explained := s.management(nil, append([]string{"explain", "--no-resolve", "--"}, args...)...)
			explained.code(t, 0)
			want, exit := "allow", 0
			if worker == "1" {
				want, exit = "deny", 1
			}
			if !bytes.HasPrefix(explained.stdout, []byte("Decision: "+want+" (mode=enforce)")) {
				t.Fatalf("environment rule explanation=%s", explained.stdout)
			}
			result := s.gh(nil, args...)
			result.code(t, exit)
			if exit == 1 {
				s.absentRecord()
				if !bytes.Contains(result.stderr, []byte("worker merges disabled")) {
					t.Fatalf("denial omitted reason: %s", result.stderr)
				}
			} else {
				s.onlyRecord(args, nil)
			}
		})
	}
}

func TestMiscEnvironmentHookSelection(t *testing.T) {
	for _, phase := range []string{config.PhasePre, config.PhasePost} {
		for _, worker := range []string{"1", "0"} {
			t.Run(phase+"/worker="+worker, func(t *testing.T) {
				s := newSandbox(t)
				s.env["MALL_GH_WORKER"] = worker
				s.configure(func(c *config.Config) {
					h := s.hook("worker", phase)
					h.Match.Env = map[string]string{"MALL_GH_WORKER": "1"}
					// Selection uses the caller's environment, not hook overrides.
					h.Env["MALL_GH_WORKER"] = "0"
					c.Hooks = []config.Hook{h}
				})
				args := []string{"pr", "merge", "7", "--admin", "-R", "acme/project"}
				s.gh(nil, args...).code(t, 0)
				s.onlyRecord(args, nil)
				if worker == "1" {
					input := s.hookInput("worker")
					if input.Event != phase || phase == config.PhasePost && (input.Result == nil || input.Result.ExitCode != 0) {
						t.Fatalf("hook input=%+v", input)
					}
				} else if _, err := os.Stat(filepath.Join(s.root, "worker.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("nonmatching hook ran: %v", err)
				}
				result := s.management(nil, "hooks", "test", "--json", "--phase", phase, "--expect", "allow", "--no-resolve", "--", "version")
				result.code(t, 0)
				var report struct {
					Results []protocol.HookResult `json:"results"`
				}
				decodeJSON(t, result.stdout, &report)
				if worker == "1" {
					if len(report.Results) != 1 || report.Results[0].HookID != "worker" || report.Results[0].Outcome != protocol.OutcomeOK {
						t.Fatalf("matching hook test results=%+v", report.Results)
					}
				} else if len(report.Results) != 0 {
					t.Fatalf("nonmatching hook test results=%+v", report.Results)
				}
			})
		}
	}
}

func TestMiscEnvironmentStdinPolicy(t *testing.T) {
	for _, worker := range []string{"1", "0"} {
		t.Run("worker="+worker, func(t *testing.T) {
			s := newSandbox(t)
			s.env["MALL_GH_WORKER"] = worker
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				c.Policy.Rules = []config.Rule{{ID: "no-worker-merges", Decision: "deny",
					Match: config.Match{Env: map[string]string{"MALL_GH_WORKER": "1"}, Operations: config.StringList{"pr.merge.*"}}}}
			})
			input := []byte(`{"query":"mutation { mergePullRequest(input:{pullRequestId:\"PR_test_node\"}) { clientMutationId } }"}`)
			args := []string{"api", "graphql", "--input", "-"}
			result := s.gh(input, args...)
			if worker == "1" {
				result.code(t, 1)
				s.absentRecord()
			} else {
				result.code(t, 0)
				s.onlyRecord(args, input)
			}
		})
	}
}

func TestMiscHooksExpect(t *testing.T) {
	for _, tc := range []struct {
		name, mode, hookDecision, ruleDecision, expect, decision string
		want                                                     int
	}{
		{"enforced denial expected", "enforce", "deny", "", "deny", "deny", 0},
		{"observe compares audit", "observe", "deny", "", "audit", "audit", 0},
		{"observe raw deny mismatches", "observe", "deny", "", "deny", "audit", 1},
		{"allow mismatch", "enforce", "deny", "", "allow", "deny", 1},
		{"policy audit", "enforce", "abstain", "audit", "audit", "audit", 0},
		{"abstain hook aggregate allows", "enforce", "abstain", "", "allow", "allow", 0},
		{"abstain expectation differs", "enforce", "abstain", "", "abstain", "allow", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = tc.mode
				h := s.hook("pre", config.PhasePre)
				h.Env["FAKEGH_HOOK_DECISION"] = tc.hookDecision
				c.Hooks = []config.Hook{h}
				if tc.ruleDecision != "" {
					c.Policy.Rules = []config.Rule{{ID: "audit", Decision: tc.ruleDecision}}
				}
			})
			result := s.management(nil, "hooks", "test", "--json", "--no-resolve", "--expect", tc.expect, "--", "version")
			result.code(t, tc.want)
			var report struct {
				Aggregate struct {
					Decision  string `json:"decision"`
					WouldDeny bool   `json:"wouldDeny"`
				} `json:"aggregate"`
			}
			decodeJSON(t, result.stdout, &report)
			if report.Aggregate.Decision != tc.decision || report.Aggregate.WouldDeny != (tc.hookDecision == "deny") {
				t.Fatalf("aggregate=%+v", report.Aggregate)
			}
			if tc.want == 1 && (!bytes.Contains(result.stderr, []byte(tc.expect)) || !bytes.Contains(result.stderr, []byte(tc.decision))) {
				t.Fatalf("missing expected/actual decision: %s", result.stderr)
			}
			s.absentRecord()
		})
	}
	for _, expect := range []string{"invalid", ""} {
		s := newSandbox(t)
		result := s.management(nil, "hooks", "test", "--expect="+expect, "--", "version")
		result.code(t, 2)
		s.absentRecord()
	}
}

func TestMiscSyntheticPostExitCode(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		h := s.hook("failed", config.PhasePost)
		h.Match.Results = config.StringList{config.ResultFailure}
		h.Match.ExitCodes = []int{17}
		c.Hooks = []config.Hook{h}
	})
	result := s.management(nil, "hooks", "test", "--phase", "post", "--exit-code", "17", "--expect", "allow", "--", "version")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("exitCode=17")) || bytes.Contains(result.stdout, []byte("successful")) {
		t.Fatalf("synthetic result description=%s", result.stdout)
	}
	if input := s.hookInput("failed"); input.Result == nil || input.Result.ExitCode != 17 {
		t.Fatalf("synthetic result=%+v", input.Result)
	}
	s.absentRecord()
}

func TestMiscDevelopmentVersionAndDoctor(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "ghok-dev")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-p=1", "-buildvcs=false", "-o", binary, "./cmd/ghok")
	build.Dir = root
	build.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dev binary: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name, path, version string
		status              install.Status
	}{
		{"development", binary, "dev", install.StatusWarn},
		{"injected release", ghokBinary, "e2e-version", install.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			result := s.run(tc.path, nil, "version")
			result.code(t, 0)
			if !bytes.HasPrefix(result.stdout, []byte("ghok "+tc.version+"\n")) {
				t.Fatalf("version=%s", result.stdout)
			}
			setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
			result = s.run(tc.path, nil, "doctor", "--json")
			result.code(t, 0)
			var checks []install.Check
			decodeJSON(t, result.stdout, &checks)
			for _, check := range checks {
				if check.Name != "ghok" {
					continue
				}
				warning := strings.Contains(check.Detail, "development build: version unknown; features may differ from the docs")
				if check.Status != tc.status || !strings.Contains(check.Detail, "(version "+tc.version+")") || warning != (tc.version == "dev") {
					t.Fatalf("ghok check=%+v", check)
				}
				return
			}
			t.Fatal("doctor omitted ghok check")
		})
	}
}
