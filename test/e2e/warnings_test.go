//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

func warningHook(s *sandbox, id, phase string, decision protocol.Decision, text string) config.Hook {
	s.t.Helper()
	h := s.hook(id, phase)
	response, err := json.Marshal(protocol.Response{SchemaVersion: 1, Decision: decision, Warning: text, Message: "blocked"})
	if err != nil {
		s.t.Fatal(err)
	}
	h.Env["FAKEGH_HOOK_RESPONSE"] = string(response)
	return h
}

func warningTransform(s *sandbox, id, text string, set map[string]any) config.Transform {
	s.t.Helper()
	t := transformFixture(s, id, nil)
	response, err := json.Marshal(protocol.TransformResponse{SchemaVersion: 1, Warning: text, Set: set})
	if err != nil {
		s.t.Fatal(err)
	}
	t.Env["FAKEGH_HOOK_RESPONSE"] = string(response)
	return t
}

func TestWarningsRunGhInOrderAndKeepDecisionLogs(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		supervised bool
		exit       int
	}{{config.ModeEnforce, false, 42}, {config.ModeObserve, true, 8}} {
		t.Run(tc.mode, func(t *testing.T) {
			s := newSandbox(t)
			secret := "ghp_" + strings.Repeat("w", 36)
			s.env["GH_TOKEN"] = secret
			s.env["FAKEGH_EXIT"] = strconv.Itoa(tc.exit)
			setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh output\n"))
			setBytes(s.env, "FAKEGH_STDERR_B64", []byte("gh diagnostic\n"))
			s.configure(func(c *config.Config) {
				c.Policy.Mode = tc.mode
				yes := true
				c.Logging.Enabled, c.Logging.Level = &yes, "decision"
				c.Policy.Rules = []config.Rule{
					{ID: "first", Decision: "warn", Reason: "review " + secret + "\ncarefully\x1b[31m"},
					{ID: "second", Decision: "warn", Reason: "whole operation"},
				}
				c.Transforms = []config.Transform{
					warningTransform(s, "rewrite", "added session id", map[string]any{"/operations/0/texts/body/value": "changed"}),
					warningTransform(s, "unchanged", "checked request", nil),
				}
				c.Hooks = []config.Hook{
					warningHook(s, "allow", config.PhasePre, protocol.DecisionAllow, "allowed advice"),
					warningHook(s, "abstain", config.PhasePre, protocol.DecisionAbstain, "abstaining advice"),
				}
				if tc.mode == config.ModeObserve {
					c.Policy.Rules = append(c.Policy.Rules, config.Rule{ID: "would-block", Decision: "deny", Reason: "silent would-deny"})
					c.Hooks = append(c.Hooks, warningHook(s, "deny", config.PhasePre, protocol.DecisionDeny, "ignored deny warning"))
				}
				if tc.supervised {
					c.Hooks = append(c.Hooks,
						warningHook(s, "post-allow", config.PhasePost, protocol.DecisionAllow, "after gh"),
						warningHook(s, "post-deny", config.PhasePost, protocol.DecisionDeny, "ignored post deny warning"),
					)
				}
			})
			args := []string{"issue", "comment", "1", "-R", "o/r", "--body", "original"}
			got := s.gh(nil, args...)
			got.code(t, tc.exit)
			args[len(args)-1] = "changed"
			s.onlyRecord(args, nil)
			want := "ghok: warning from rule first\n  review [REDACTED]\n  carefully\\x1b[31m\n" +
				"ghok: warning from rule second\n  whole operation\n" +
				"ghok: warning from transform rewrite\n  added session id\n" +
				"ghok: warning from transform unchanged\n  checked request\n" +
				"ghok: warning from hook allow\n  allowed advice\n" +
				"ghok: warning from hook abstain\n  abstaining advice\n" +
				"gh diagnostic\n"
			warnings := []protocol.Warning{
				{Source: "rule", ID: "first", Text: "review [REDACTED]\ncarefully\x1b[31m"},
				{Source: "rule", ID: "second", Text: "whole operation"},
				{Source: "transform", ID: "rewrite", Text: "added session id"},
				{Source: "transform", ID: "unchanged", Text: "checked request"},
				{Source: "hook", ID: "allow", Text: "allowed advice"},
				{Source: "hook", ID: "abstain", Text: "abstaining advice"},
			}
			if tc.supervised {
				want += "ghok: warning from hook post-allow\n  after gh\n"
				warnings = append(warnings, protocol.Warning{Source: "hook", ID: "post-allow", Text: "after gh"})
			}
			if string(got.stderr) != want || string(got.stdout) != "gh output\n" {
				t.Fatalf("stdout=%q stderr=%q; want stderr=%q", got.stdout, got.stderr, want)
			}
			logs := s.logs()
			if len(logs) != 1 || !reflect.DeepEqual(logs[0].Warnings, warnings) {
				t.Fatalf("warning log=%+v; want=%+v", logs, warnings)
			}
			decision := "allow"
			if tc.mode == config.ModeObserve {
				decision = "would-deny"
			}
			if logs[0].Decision != decision || s.hookInput("allow").Policy.Decision == "warn" {
				t.Fatalf("decision changed: %+v", logs[0])
			}
		})
	}
}

func TestWarningsDoNotChangeDenialBytes(t *testing.T) {
	s := newSandbox(t)
	configure := func(warn bool) {
		s.configure(func(c *config.Config) {
			c.Policy.Mode = config.ModeEnforce
			c.Policy.Rules = []config.Rule{{ID: "gate", Decision: "deny", Reason: "blocked"}}
			if warn {
				c.Policy.Rules = append([]config.Rule{{ID: "reminder", Decision: "warn", Reason: "rule advice"}}, c.Policy.Rules...)
				c.Transforms = []config.Transform{warningTransform(s, "annotation", "transform advice", nil)}
				c.Hooks = []config.Hook{warningHook(s, "advice", config.PhasePre, protocol.DecisionAbstain, "hook advice")}
			}
		})
	}
	configure(false)
	baseline := s.gh(nil, "version")
	baseline.code(t, 77)
	configure(true)
	warned := s.gh(nil, "version")
	warned.code(t, 77)
	if !bytes.Equal(baseline.stderr, warned.stderr) || !bytes.Equal(baseline.stdout, warned.stdout) {
		t.Fatalf("denial changed: baseline=%q warned=%q", baseline.stderr, warned.stderr)
	}
	s.absentRecord()
}

func TestDenyResponseWarningIgnored(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		c.Hooks = []config.Hook{warningHook(s, "gate", config.PhasePre, protocol.DecisionDeny, "not denial text")}
	})
	got := s.gh(nil, "version")
	got.code(t, 77)
	if want := "ghok: denied by hook gate\n  - hook gate: blocked\n"; string(got.stderr) != want {
		t.Fatalf("stderr=%q, want=%q", got.stderr, want)
	}
	s.absentRecord()
}

func TestWarningManagementReports(t *testing.T) {
	for _, command := range []string{"explain", "hooks"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(command+"/json="+strconv.FormatBool(asJSON), func(t *testing.T) {
				s := newSandbox(t)
				secret := "ghp_" + strings.Repeat("m", 36)
				s.env["GH_TOKEN"] = secret
				s.configure(func(c *config.Config) {
					c.Policy.Mode = config.ModeEnforce
					c.Policy.Rules = []config.Rule{{ID: "reminder", Decision: "warn", Reason: "rule " + secret}}
					c.Transforms = []config.Transform{warningTransform(s, "annotation", "transform advice", nil)}
					c.Hooks = []config.Hook{warningHook(s, "advice", config.PhasePre, protocol.DecisionAbstain, "hook advice")}
				})
				args := []string{command}
				if command == "hooks" {
					args = append(args, "test", "--expect", "allow", "--expect-hooks", "abstain", "--expect-policy", "allow")
				}
				if asJSON {
					args = append(args, "--json")
				}
				got := s.management(nil, append(args, "--", "version")...)
				got.code(t, 0)
				warnings := []protocol.Warning{{Source: "rule", ID: "reminder", Text: "rule [REDACTED]"}, {Source: "transform", ID: "annotation", Text: "transform advice"}}
				if command == "hooks" {
					warnings = append(warnings, protocol.Warning{Source: "hook", ID: "advice", Text: "hook advice"})
				}
				if asJSON {
					var report struct {
						Warnings []protocol.Warning `json:"warnings"`
					}
					decodeJSON(t, got.stdout, &report)
					if !reflect.DeepEqual(report.Warnings, warnings) {
						t.Fatalf("warnings=%+v, want=%+v", report.Warnings, warnings)
					}
				} else {
					for _, warning := range warnings {
						if !bytes.Contains(got.stdout, []byte(warning.Source+" "+warning.ID+":")) || !bytes.Contains(got.stdout, []byte(warning.Text)) {
							t.Fatalf("missing warning %+v: %s", warning, got.stdout)
						}
					}
				}
				if bytes.Contains(got.stdout, []byte(secret)) || bytes.Contains(got.stderr, []byte("warning from")) {
					t.Fatalf("unsafe diagnostic: stdout=%s stderr=%s", got.stdout, got.stderr)
				}
				s.absentRecord()
			})
		}
	}
}

func TestContinuedWarningKeepsDecisionLog(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		yes := true
		c.Logging.Enabled, c.Logging.Level = &yes, "decision"
		h := warningHook(s, "advice", config.PhasePre, protocol.DecisionDeny, "ignored warning")
		h.ContinueOnError = true
		c.Hooks = []config.Hook{h}
	})
	got := s.gh(nil, "version")
	got.code(t, 0)
	if want := "ghok: warning from hook advice (continue-on-error)\n  blocked\n"; string(got.stderr) != want {
		t.Fatalf("stderr=%q, want=%q", got.stderr, want)
	}
	logs := s.logs()
	want := []protocol.Warning{{Source: "hook", ID: "advice", Text: "blocked"}}
	if len(logs) != 1 || logs[0].Decision != "allow" || !reflect.DeepEqual(logs[0].Warnings, want) {
		t.Fatalf("continued warning dropped or replaced by deny response warning: %+v", logs)
	}
}
