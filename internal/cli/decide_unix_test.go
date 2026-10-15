//go:build unix

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestDenialOrderingAndStatus(t *testing.T) {
	evaluation := policy.Evaluation{Decision: protocol.DecisionDeny, Denials: []policy.Denial{
		{Kind: policy.DenialRule, ID: "first", Reason: "rule reason"},
		{Kind: policy.DenialPolicy, ID: "unknownMutation", Reason: "policy reason"},
	}}
	pre := hooks.Aggregate{Decision: protocol.DecisionDeny, Results: []protocol.HookResult{
		{HookID: "failed", Outcome: protocol.OutcomeError, Effective: protocol.DecisionDeny, ExitCode: 23, Error: "exit status 23"},
		{HookID: "content", Outcome: protocol.OutcomeOK, Effective: protocol.DecisionDeny, Message: "first line\nsecret continuation", Reason: "unused reason"},
		{HookID: "ignored", Outcome: protocol.OutcomeTimeout, Effective: protocol.DecisionAllow},
	}}
	got := decide(config.ModeEnforce, evaluation, pre, false)
	var stderr bytes.Buffer
	printDenials(&stderr, got.Denials, func(s string) string { return strings.ReplaceAll(s, "secret", "[REDACTED]") })
	want := "ghok: denied by rule first (+3 more)\n  - rule first: rule reason\n  - policy unknownMutation: policy reason\n  - hook content: first line\n    [REDACTED] continuation\n  - hook failed: exit status 23\n"
	if got.ExitCode != 77 || stderr.String() != want {
		t.Fatalf("status=%d stderr=%q", got.ExitCode, stderr.String())
	}
}

func TestHookErrorDenialKinds(t *testing.T) {
	for _, tc := range []struct {
		outcome protocol.Outcome
		code    int
		kind    string
	}{
		{protocol.OutcomeTimeout, -1, "timeout"},
		{protocol.OutcomeError, 23, "exit 23"},
		{protocol.OutcomeInvalidResponse, 0, "invalid response"},
		{protocol.OutcomeError, -1, "spawn failed"},
		{protocol.OutcomeCanceled, -1, "canceled"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			pre := hooks.Aggregate{Decision: protocol.DecisionDeny, Results: []protocol.HookResult{{HookID: "gate", Outcome: tc.outcome, ExitCode: tc.code, Effective: protocol.DecisionDeny, Error: "detail"}}}
			got := decide(config.ModeEnforce, policy.Evaluation{}, pre, false)
			var stderr bytes.Buffer
			printDenials(&stderr, got.Denials, func(s string) string { return s })
			want := "ghok: denied by hook gate (error: " + tc.kind + ")\n  - hook gate: detail\n"
			if got.ExitCode != 78 || stderr.String() != want {
				t.Fatalf("status=%d stderr=%q", got.ExitCode, stderr.String())
			}
		})
	}
}

func TestObserveDoesNotEnforceDenials(t *testing.T) {
	got := decide(config.ModeObserve, policy.Evaluation{WouldDeny: true}, hooks.Aggregate{}, false)
	if got.Decision != "would-deny" || got.ExitCode != 0 || len(got.Denials) != 0 {
		t.Fatalf("observe=%+v", got)
	}
}

func TestSkippedStdinPolicyRelabel(t *testing.T) {
	got := decide(config.ModeObserve, policy.Evaluation{Decision: protocol.DecisionAudit}, hooks.Aggregate{}, true)
	if got.Decision != "allow" || !got.RelabelPolicy {
		t.Fatalf("skipped stdin=%+v", got)
	}
}

func TestDenialRedactionPreservesGrammar(t *testing.T) {
	for _, secret := range []string{"ghok", "hook", "error", "timeout"} {
		t.Run(secret, func(t *testing.T) {
			var stderr bytes.Buffer
			printDenials(&stderr, []denial{{Source: "hook", ID: secret + "\r\ninjected", Message: secret, ErrorKind: "timeout"}}, func(s string) string {
				return strings.ReplaceAll(s, secret, "[REDACTED]")
			})
			want := "ghok: denied by hook [REDACTED]\\x0d\\ninjected (error: timeout)\n  - hook [REDACTED]\\x0d\\ninjected: [REDACTED]\n"
			if stderr.String() != want {
				t.Fatalf("stderr=%q, want=%q", stderr.String(), want)
			}
		})
	}
}

func TestErrorDenialShowsStderrTail(t *testing.T) {
	pre := hooks.Aggregate{Decision: protocol.DecisionDeny, Results: []protocol.HookResult{
		{HookID: "gate", Outcome: protocol.OutcomeError, Effective: protocol.DecisionDeny, ExitCode: 127, Started: true, Error: "exit status 127", Stderr: "starting\n\nloading config\nenv: bun: No such file or directory\n"},
		{HookID: "secretive", Outcome: protocol.OutcomeTimeout, Effective: protocol.DecisionDeny, Error: "context deadline exceeded", Stderr: "token leaked here\n"},
	}}
	got := decide(config.ModeEnforce, policy.Evaluation{}, pre, false)
	attachHookStderr(got.Denials, pre, []config.Hook{{ID: "gate"}, {ID: "secretive", RedactInput: true}})
	var stderr bytes.Buffer
	printDenials(&stderr, got.Denials, func(s string) string { return s })
	out := stderr.String()
	for _, want := range []string{
		"ghok: denied by hook gate (error: exit 127) (+1 more)",
		"      stderr: loading config\n      stderr: env: bun: No such file or directory",
		"      stderr: (withheld; redactInput)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "starting") || strings.Contains(out, "token leaked") {
		t.Fatalf("only the last 2 lines, and nothing for redactInput hooks:\n%s", out)
	}
	if got.ExitCode != 78 {
		t.Fatalf("hook malfunction exit = %d, want 78", got.ExitCode)
	}
}

func TestStderrTailCapsLineLength(t *testing.T) {
	got := stderrTail(strings.Repeat("x", 500), 2, 200)
	if len(got) != 1 || len(got[0]) != 200+len("…") {
		t.Fatalf("got %q", got)
	}
}
