//go:build unix

package cli

import (
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestDecide(t *testing.T) {
	policyDeny := policy.Evaluation{Decision: protocol.DecisionDeny, DenyReasons: []string{"policy one", "policy two"}}
	hookDeny := hooks.Aggregate{
		Decision: protocol.DecisionDeny,
		Results: []protocol.HookResult{
			{Effective: protocol.DecisionAllow, Reason: "allowed"},
			{Effective: protocol.DecisionDeny, Reason: "first denial", Error: "unused error"},
			{Effective: protocol.DecisionDeny, Reason: "second denial"},
		},
		Message: "hook message",
	}
	errorDeny := hooks.Aggregate{
		Decision: protocol.DecisionDeny,
		Results:  []protocol.HookResult{{Effective: protocol.DecisionDeny, Error: "hook failed"}},
		Message:  "hook failed",
	}
	duplicateDeny := hookDeny
	duplicateDeny.Message = "first denial"
	emptyFirstDeny := hooks.Aggregate{
		Decision: protocol.DecisionDeny,
		Results: []protocol.HookResult{
			{Effective: protocol.DecisionDeny},
			{Effective: protocol.DecisionDeny, Reason: "not the first denial"},
		},
	}
	for _, tt := range []struct {
		name         string
		mode         string
		evaluation   policy.Evaluation
		pre          hooks.Aggregate
		stdinSkipped bool
		want         wrapperDecision
	}{
		{
			name: "enforce allow", mode: config.ModeEnforce,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAllow},
			want:       wrapperDecision{Decision: "allow"},
		},
		{
			name: "enforce policy denial", mode: config.ModeEnforce, evaluation: policyDeny,
			want: wrapperDecision{Decision: "deny", Reason: "policy one; policy two", Prefix: "ghok: denied by policy:"},
		},
		{
			name: "enforce hook first effective denial", mode: config.ModeEnforce, pre: hookDeny,
			want: wrapperDecision{Decision: "deny", Reason: "first denial; hook message", Prefix: "ghok: denied:"},
		},
		{
			name: "mixed policy and hook denial", mode: config.ModeEnforce, evaluation: policyDeny, pre: hookDeny,
			want: wrapperDecision{Decision: "deny", Reason: "policy one; policy two; first denial; hook message", Prefix: "ghok: denied by policy:"},
		},
		{
			name: "hook error and duplicate message", mode: config.ModeEnforce, pre: errorDeny,
			want: wrapperDecision{Decision: "deny", Reason: "hook failed", Prefix: "ghok: denied:"},
		},
		{
			name: "hook reason and duplicate message", mode: config.ModeEnforce, pre: duplicateDeny,
			want: wrapperDecision{Decision: "deny", Reason: "first denial", Prefix: "ghok: denied:"},
		},
		{
			name: "empty first denial does not select later reason", mode: config.ModeEnforce, pre: emptyFirstDeny,
			want: wrapperDecision{Decision: "deny", Reason: "pre-execution policy", Prefix: "ghok: denied:"},
		},
		{
			name: "deny fallback", mode: config.ModeEnforce,
			evaluation: policy.Evaluation{WouldDeny: true, Reasons: []string{"diagnostic only"}},
			want:       wrapperDecision{Decision: "deny", Reason: "pre-execution policy", Prefix: "ghok: denied:"},
		},
		{
			name: "observe mixed denial", mode: config.ModeObserve, evaluation: policyDeny, pre: hookDeny,
			want: wrapperDecision{Decision: "would-deny"},
		},
		{
			name: "observe would deny flag", mode: config.ModeObserve,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAudit, WouldDeny: true},
			want:       wrapperDecision{Decision: "would-deny"},
		},
		{
			name: "observe hook denial", mode: config.ModeObserve, pre: hookDeny,
			want: wrapperDecision{Decision: "would-deny"},
		},
		{
			name: "enforce audit", mode: config.ModeEnforce,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAudit},
			want:       wrapperDecision{Decision: "audit"},
		},
		{
			name: "observe audit", mode: config.ModeObserve,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAudit},
			want:       wrapperDecision{Decision: "audit"},
		},
		{
			name: "skipped stdin relabels audit", mode: config.ModeObserve, stdinSkipped: true,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAudit},
			want:       wrapperDecision{Decision: "allow", RelabelPolicy: true},
		},
		{
			name: "skipped stdin relabels implicit observe denial", mode: config.ModeObserve, stdinSkipped: true,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAudit, WouldDeny: true},
			want:       wrapperDecision{Decision: "allow", RelabelPolicy: true},
		},
		{
			name: "skipped stdin preserves explicit audit rule", mode: config.ModeObserve, stdinSkipped: true,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAudit, Rules: []string{"audit-rule"}},
			want:       wrapperDecision{Decision: "audit"},
		},
		{
			name: "skipped stdin preserves explicit observe denial", mode: config.ModeObserve, stdinSkipped: true,
			evaluation: policy.Evaluation{Decision: protocol.DecisionAllow, WouldDeny: true, Rules: []string{"deny-rule"}},
			want:       wrapperDecision{Decision: "would-deny"},
		},
		{
			name: "skipped stdin preserves hook observe denial", mode: config.ModeObserve, stdinSkipped: true, pre: hookDeny,
			want: wrapperDecision{Decision: "would-deny"},
		},
		{
			name: "skipped stdin preserves enforcing denial", mode: config.ModeEnforce, stdinSkipped: true, evaluation: policyDeny,
			want: wrapperDecision{Decision: "deny", Reason: "policy one; policy two", Prefix: "ghok: denied by policy:"},
		},
		{
			name: "skipped stdin preserves enforcing hook denial", mode: config.ModeEnforce, stdinSkipped: true, pre: errorDeny,
			want: wrapperDecision{Decision: "deny", Reason: "hook failed", Prefix: "ghok: denied:"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := decide(tt.mode, tt.evaluation, tt.pre, tt.stdinSkipped); got != tt.want {
				t.Fatalf("decide() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
