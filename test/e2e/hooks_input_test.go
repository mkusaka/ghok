//go:build unix

package e2e

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

func TestHooksRawInput(t *testing.T) {
	for _, phase := range []string{"pre", "post"} {
		t.Run(phase, func(t *testing.T) {
			s := newSandbox(t)
			secret := "ghp_" + strings.Repeat("r", 36)
			s.env["GH_TOKEN"] = secret
			s.configure(func(c *config.Config) {
				c.Hooks = []config.Hook{s.hook(phase, phase)}
			})
			args := []string{"api", "repos/acme/project/issues", "-f", "title=example", "-f", "body=" + secret}
			s.gh(nil, args...).code(t, 0)
			s.onlyRecord(args, nil)
			input := s.hookInput(phase)
			if !reflect.DeepEqual(input.Invocation.Argv, args) || !reflect.DeepEqual(input.Invocation.Expanded, args) {
				t.Fatalf("hook argv changed: argv=%q expanded=%q", input.Invocation.Argv, input.Invocation.Expanded)
			}
			op := input.Operations[0]
			if op.Texts == nil || op.Texts.Body == nil || op.Texts.Body.Value != secret || op.Parameters["body"] != secret {
				t.Fatalf("hook did not receive raw body: %+v", op)
			}
		})
	}
}

func TestHooksRedactInputIsolationAndLogs(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(strconv.Itoa(concurrency), func(t *testing.T) {
			s := newSandbox(t)
			secret := "learned-" + "hook-input-credential"
			pattern := "ghp_" + strings.Repeat("s", 36)
			s.env["GH_TOKEN"] = secret
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				c.Runtime.HookConcurrency = concurrency
				masked := s.hook("masked", "pre")
				masked.RedactInput = true
				c.Hooks = []config.Hook{masked, s.hook("raw", "pre")}
			})
			body := secret + " and " + pattern + " and " + secret
			args := []string{"pr", "create", "-R", "acme/project", "-t", "ordinary title", "-b", body}
			s.gh(nil, args...).code(t, 0)
			s.onlyRecord(args, nil)
			raw, masked := s.hookInput("raw"), s.hookInput("masked")
			if raw.Redactions != 0 || raw.Operations[0].Texts.Body.Value != body || raw.Operations[0].Texts.Body.Redactions != 0 || !reflect.DeepEqual(raw.Invocation.Argv, args) {
				t.Fatalf("redaction mutated another hook's input: %+v", raw)
			}
			text := masked.Operations[0].Texts.Body
			if text.Value != "[REDACTED] and [REDACTED] and [REDACTED]" || text.Redactions != 3 || text.Bytes != int64(len(body)) {
				t.Fatalf("masked text/count/size = %+v", text)
			}
			if masked.Operations[0].Texts.Title.Redactions != 0 || masked.Redactions != 9 || masked.Invocation.Argv[len(args)-1] != text.Value || masked.Invocation.Expanded[len(args)-1] != text.Value {
				t.Fatalf("masked input/count = %+v", masked)
			}
			data, err := os.ReadFile(filepath.Join(s.root, "masked.json"))
			if err != nil {
				t.Fatal(err)
			}
			var fields struct {
				Operations []struct {
					Texts struct {
						Title map[string]any `json:"title"`
					} `json:"texts"`
				} `json:"operations"`
			}
			decodeJSON(t, data, &fields)
			if count, present := fields.Operations[0].Texts.Title["redactions"]; !present || count != float64(0) {
				t.Fatalf("unmasked Text must report zero redactions: %s", data)
			}
			for _, path := range []string{filepath.Join(s.root, "masked.json"), filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl")} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(pattern)) {
					t.Fatalf("credential exposed in %s", filepath.Base(path))
				}
			}
			logs := s.logs()
			if len(logs) != 1 || logs[0].Redactions <= 0 {
				t.Fatalf("log omitted redaction count: %+v", logs)
			}
		})
	}
}

func TestHooksRawDiagnosticsStayRedacted(t *testing.T) {
	s := newSandbox(t)
	secret := "ghp_" + strings.Repeat("d", 36)
	s.env["GH_TOKEN"] = secret
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		yes := true
		c.Logging.Enabled = &yes
		h := s.hook("scan", "pre")
		h.Env["FAKEGH_HOOK_RESPONSE"] = `{"decision":"deny","reason":"` + secret + `","message":"` + secret + `"}`
		setBytes(h.Env, "FAKEGH_HOOK_STDERR_B64", []byte(secret))
		c.Hooks = []config.Hook{h}
	})
	result := s.gh(nil, "pr", "create", "-t", "T", "-b", secret)
	result.code(t, 77)
	s.absentRecord()
	if bytes.Contains(result.stderr, []byte(secret)) || !bytes.Contains(result.stderr, []byte("[REDACTED]")) {
		t.Fatalf("raw hook exposed denial diagnostic: %s", result.stderr)
	}
	log := s.logs()[0]
	if log.Hooks[0].Reason != "[REDACTED]" || log.Hooks[0].Message != "[REDACTED]" || log.Hooks[0].Stderr != "[REDACTED]" {
		t.Fatalf("hook diagnostics were not redacted: %+v", log.Hooks[0])
	}
}

func TestHooksTestDecisionsAndExpectations(t *testing.T) {
	for _, tc := range []struct {
		name, mode, hook, rule, wantHooks, wantPolicy, wantFinal string
		noMatch, wouldDeny                                       bool
	}{
		{name: "no-hooks", mode: "enforce", wantHooks: "abstain", wantPolicy: "allow", wantFinal: "allow"},
		{name: "no-match", mode: "enforce", hook: "deny", noMatch: true, wantHooks: "abstain", wantPolicy: "allow", wantFinal: "allow"},
		{name: "all-abstain", mode: "enforce", hook: "abstain", wantHooks: "abstain", wantPolicy: "allow", wantFinal: "allow"},
		{name: "some-allow", mode: "enforce", hook: "allow", wantHooks: "allow", wantPolicy: "allow", wantFinal: "allow"},
		{name: "hook-deny", mode: "enforce", hook: "deny", wantHooks: "deny", wantPolicy: "allow", wantFinal: "deny", wouldDeny: true},
		{name: "observe-hook-deny", mode: "observe", hook: "deny", wantHooks: "deny", wantPolicy: "allow", wantFinal: "audit", wouldDeny: true},
		{name: "policy-deny", mode: "enforce", hook: "allow", rule: "deny", wantHooks: "allow", wantPolicy: "deny", wantFinal: "deny", wouldDeny: true},
		{name: "policy-audit", mode: "enforce", hook: "abstain", rule: "audit", wantHooks: "abstain", wantPolicy: "audit", wantFinal: "audit"},
		{name: "observe-policy-deny", mode: "observe", rule: "deny", wantHooks: "abstain", wantPolicy: "audit", wantFinal: "audit", wouldDeny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = tc.mode
				if tc.hook != "" {
					h := s.hook("decision", "pre")
					h.Env["FAKEGH_HOOK_DECISION"] = tc.hook
					if tc.noMatch {
						h.Match.Commands = config.StringList{"pr merge"}
					}
					c.Hooks = []config.Hook{h, s.hook("abstain", "pre")}
					if tc.noMatch {
						c.Hooks = c.Hooks[:1]
					}
				}
				if tc.rule != "" {
					c.Policy.Rules = []config.Rule{{ID: "decision", Decision: tc.rule}}
				}
			})
			args := []string{"hooks", "test", "--no-resolve", "--expect-hooks", tc.wantHooks, "--expect-policy", tc.wantPolicy, "--expect", tc.wantFinal}
			result := s.management(nil, append(args, "--json", "--", "version")...)
			result.code(t, 0)
			var report struct {
				Aggregate struct {
					Decision string `json:"decision"`
				} `json:"aggregate"`
				Policy struct {
					Decision string `json:"decision"`
				} `json:"policy"`
				Final struct {
					Decision  string `json:"decision"`
					WouldDeny bool   `json:"wouldDeny"`
				} `json:"final"`
			}
			decodeJSON(t, result.stdout, &report)
			if report.Aggregate.Decision != tc.wantHooks || report.Policy.Decision != tc.wantPolicy || report.Final.Decision != tc.wantFinal || report.Final.WouldDeny != tc.wouldDeny {
				t.Fatalf("decisions = %+v", report)
			}
			result = s.management(nil, append(args, "--", "version")...)
			result.code(t, 0)
			for _, label := range []string{"Hooks: " + tc.wantHooks, "Policy: " + tc.wantPolicy, "FINAL: " + tc.wantFinal} {
				if !bytes.Contains(result.stdout, []byte(label)) {
					t.Fatalf("decision missing from human report: %s", result.stdout)
				}
			}
			s.absentRecord()
		})
	}
}

func TestHooksTestExpectationFailures(t *testing.T) {
	s := newSandbox(t)
	result := s.management(nil, "hooks", "test", "--expect", "deny", "--expect-hooks", "allow", "--expect-policy", "deny", "--", "version")
	result.code(t, 1)
	for _, name := range []string{"FINAL", "hooks", "policy"} {
		if !bytes.Contains(result.stderr, []byte(name)) {
			t.Fatalf("failed %s expectation not identified: %s", name, result.stderr)
		}
	}
	for _, args := range [][]string{
		{"--expect-hooks", "audit"}, {"--expect-hooks="},
		{"--expect-policy", "abstain"}, {"--expect-policy="},
	} {
		result = s.management(nil, append(append([]string{"hooks", "test"}, args...), "--", "version")...)
		result.code(t, 2)
	}
	s.absentRecord()
}

func TestHooksTestRawInput(t *testing.T) {
	s := newSandbox(t)
	secret := "ghp_" + strings.Repeat("m", 36)
	s.env["GH_TOKEN"] = secret
	s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("raw", "pre")} })
	args := []string{"pr", "create", "-t", "T", "-b", secret}
	result := s.management(nil, append([]string{"hooks", "test", "--expect-hooks", "abstain", "--"}, args...)...)
	result.code(t, 0)
	input := s.hookInput("raw")
	if !reflect.DeepEqual(input.Invocation.Argv, args) || input.Operations[0].Texts.Body.Value != secret {
		t.Fatalf("hooks test redacted trusted input: %+v", input)
	}
	if bytes.Contains(result.stdout, []byte(secret)) || bytes.Contains(result.stderr, []byte(secret)) {
		t.Fatal("hooks test displayed a credential")
	}
	s.absentRecord()
}

func TestHooksRawCapturedOutput(t *testing.T) {
	s := newSandbox(t)
	secret := "ghp_" + strings.Repeat("o", 36)
	s.env["GH_TOKEN"] = secret
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte(secret))
	s.configure(func(c *config.Config) {
		h := s.hook("capture", "post")
		h.CaptureOutput = true
		c.Hooks = []config.Hook{h}
	})
	result := s.gh(nil, "version")
	result.code(t, 0)
	input := s.hookInput("capture")
	if input.Result == nil || input.Result.Stdout == nil {
		t.Fatal("capture-requesting hook did not receive output")
	}
	body, err := base64.StdEncoding.DecodeString(input.Result.Stdout.Base64)
	if err != nil || string(body) != secret || input.Redactions != 0 || input.Result.Stdout.Size != int64(len(secret)) {
		t.Fatalf("raw captured output changed: %q, %+v, %v", body, input.Result, err)
	}
}
