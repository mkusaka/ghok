//go:build unix

package e2e

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestContinueOnErrorRunsGhAndPreservesStatus(t *testing.T) {
	for _, tc := range []struct {
		name, mode                           string
		malfunction, redactInput, supervised bool
		exit                                 int
	}{
		{"content-exec", config.ModeEnforce, false, false, false, 42},
		{"content-observe-supervised", config.ModeObserve, false, false, true, 8},
		{"error-exec", config.ModeEnforce, true, false, false, 4},
		{"error-withheld-supervised", config.ModeEnforce, true, true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			secret := "ghp_" + strings.Repeat("w", 36)
			s.env["GH_TOKEN"] = secret
			s.env["FAKEGH_EXIT"] = strconv.Itoa(tc.exit)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = tc.mode
				yes := true
				c.Logging.Enabled = &yes
				h := s.hook("advice", config.PhasePre)
				h.ContinueOnError, h.RedactInput = true, tc.redactInput
				h.OnError = config.OnErrorDeny
				h.Env["FAKEGH_HOOK_DECISION"] = "deny"
				h.Env["FAKEGH_HOOK_MESSAGE"] = "review required\n" + secret + "\x1b[31m"
				if tc.malfunction {
					h.Env["FAKEGH_HOOK_EXIT"] = "23"
					setBytes(h.Env, "FAKEGH_HOOK_STDERR_B64", []byte("discarded\nloading\nmissing tool "+secret+"\n"))
				}
				dependent := s.hook("dependent", config.PhasePre)
				dependent.DependsOn = []string{"advice"}
				c.Hooks = []config.Hook{h, dependent}
				if tc.supervised {
					c.Hooks = append(c.Hooks, s.hook("audit", config.PhasePost))
				}
			})
			args := []string{"pr", "view", "1", "-R", "o/r"}
			got := s.gh(nil, args...)
			got.code(t, tc.exit)
			s.onlyRecord(args, nil)
			want := "ghok: warning from hook advice (continue-on-error)\n  review required\n  [REDACTED]\\x1b[31m\n"
			outcome, decision := protocol.OutcomeOK, protocol.DecisionDeny
			if tc.malfunction {
				outcome, decision = protocol.OutcomeError, ""
				want = "ghok: warning from hook advice (continue-on-error)\n  exit status 23\n"
				if tc.redactInput {
					want += "    stderr: (withheld; redactInput)\n"
				} else {
					want += "    stderr: loading\n    stderr: missing tool [REDACTED]\n"
				}
			}
			if string(got.stderr) != want {
				t.Fatalf("stderr=%q, want=%q", got.stderr, want)
			}
			previous := s.hookInput("dependent").Previous
			if len(previous) != 1 || previous[0].Outcome != outcome || previous[0].Decision != decision || previous[0].Effective != protocol.DecisionAbstain || !previous[0].ContinuedOnError {
				t.Fatalf("dependent previous=%+v", previous)
			}
			logs := s.logs()
			if len(logs) != 1 || logs[0].Decision != "allow" || len(logs[0].Hooks) < 2 {
				t.Fatalf("invocation logs=%+v", logs)
			}
			result := logs[0].Hooks[0]
			if result.Outcome != outcome || result.Decision != decision || result.Effective != protocol.DecisionAbstain || !result.ContinuedOnError {
				t.Fatalf("logged hook=%+v", result)
			}
			if tc.supervised {
				if post := s.hookInput("audit").Result; post == nil || post.ExitCode != tc.exit {
					t.Fatalf("post result=%+v", post)
				}
			}
		})
	}
}

func TestContinueOnErrorSuppressesWarningWhenDenied(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		yes := true
		c.Logging.Enabled = &yes
		advice := s.hook("advice", config.PhasePre)
		advice.ContinueOnError = true
		advice.Env["FAKEGH_HOOK_DECISION"] = "deny"
		advice.Env["FAKEGH_HOOK_MESSAGE"] = "nonblocking review"
		gate := s.hook("gate", config.PhasePre)
		gate.Env["FAKEGH_HOOK_DECISION"] = "deny"
		gate.Env["FAKEGH_HOOK_MESSAGE"] = "blocking review"
		c.Hooks = []config.Hook{advice, gate}
	})
	got := s.gh(nil, "version")
	got.code(t, 77)
	s.absentRecord()
	if want := "ghok: denied by hook gate\n  - hook gate: blocking review\n"; string(got.stderr) != want {
		t.Fatalf("stderr=%q, want=%q", got.stderr, want)
	}
	logs := s.logs()
	if len(logs) != 1 || logs[0].Decision != "deny" || len(logs[0].Hooks) != 2 || !logs[0].Hooks[0].ContinuedOnError {
		t.Fatalf("continued denial missing from log: %+v", logs)
	}
}

func TestContinueOnErrorManagementVisibility(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		h := s.hook("advice", config.PhasePre)
		h.ContinueOnError = true
		h.Env["FAKEGH_HOOK_DECISION"] = "deny"
		c.Hooks = []config.Hook{h}
	})
	listed := s.management(nil, "hooks", "list")
	listed.code(t, 0)
	if !bytes.Contains(listed.stdout, []byte("continueOnError=true")) {
		t.Fatalf("hooks list: %s", listed.stdout)
	}
	listed = s.management(nil, "hooks", "list", "--json")
	listed.code(t, 0)
	var hooks []struct {
		ContinueOnError bool `json:"continueOnError"`
	}
	decodeJSON(t, listed.stdout, &hooks)
	if len(hooks) != 1 || !hooks[0].ContinueOnError {
		t.Fatalf("hooks list JSON: %+v", hooks)
	}
	for _, asJSON := range []bool{false, true} {
		args := []string{"hooks", "test"}
		if asJSON {
			args = append(args, "--json")
		}
		args = append(args, "--", "version")
		got := s.management(nil, args...)
		got.code(t, 0)
		if !bytes.Contains(got.stdout, []byte(`"continuedOnError":true`)) && !bytes.Contains(got.stdout, []byte(`"continuedOnError": true`)) {
			t.Fatalf("continued result not shown: %s", got.stdout)
		}
		if asJSON {
			var report struct {
				Results []protocol.HookResult `json:"results"`
				Final   struct {
					Decision protocol.Decision `json:"decision"`
				} `json:"final"`
			}
			decodeJSON(t, got.stdout, &report)
			if len(report.Results) != 1 || report.Results[0].Decision != protocol.DecisionDeny || report.Results[0].Effective != protocol.DecisionAbstain || report.Final.Decision != protocol.DecisionAllow {
				t.Fatalf("hooks test report: %+v", report)
			}
		}
		if bytes.Contains(got.stderr, []byte("warning from")) {
			t.Fatalf("hooks test must not print gh-runs-only warnings: %s", got.stderr)
		}
	}
	s.absentRecord()
}
