package policy

import (
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestUnknownInputIndependentOfUnknownMutation(t *testing.T) {
	for _, reason := range []model.UnknownReason{model.UnknownInputUnavailable, model.UnknownInputTooLarge, model.UnknownMutation, model.UnknownParseError} {
		for _, tc := range []struct {
			input, mutation                 string
			inputDecision, mutationDecision protocol.Decision
		}{
			{"allow", "deny", protocol.DecisionAllow, protocol.DecisionDeny},
			{"deny", "allow", protocol.DecisionDeny, protocol.DecisionAllow},
			{"", "deny", protocol.DecisionAudit, protocol.DecisionDeny},
		} {
			t.Run(string(reason)+"/"+tc.input+"/"+tc.mutation, func(t *testing.T) {
				op := model.NewUnknown(model.LevelL3, reason, "stdin not captured (timeout)").Operations[0]
				got := Evaluate(config.Policy{Mode: config.ModeEnforce, UnknownInput: tc.input, UnknownMutation: tc.mutation}, subject(op))
				want := tc.mutationDecision
				if reason == model.UnknownInputUnavailable || reason == model.UnknownInputTooLarge {
					want = tc.inputDecision
				}
				if got.Decision != want {
					t.Fatalf("decision=%s, want=%s: %+v", got.Decision, want, got)
				}
				if want == protocol.DecisionDeny && reason == model.UnknownInputUnavailable && (len(got.DenyReasons) != 1 || !strings.Contains(got.DenyReasons[0], "timeout")) {
					t.Fatalf("missing denial cause: %+v", got)
				}
			})
		}
	}
}

func TestUnknownInputCannotBypassRulesOrProtectedTargets(t *testing.T) {
	op := model.NewUnknown(model.LevelL3, model.UnknownInputUnavailable, "unreadable request").Operations[0]
	for _, p := range []config.Policy{
		{Mode: config.ModeEnforce, UnknownInput: "allow", Rules: []config.Rule{{ID: "deny", Decision: "deny"}}},
		{Mode: config.ModeEnforce, UnknownInput: "allow", ProtectedRepositories: []string{"org/*"}},
	} {
		if got := Evaluate(p, subject(op)); got.Decision != protocol.DecisionDeny {
			t.Fatalf("other deny bypassed: %+v", got)
		}
	}
	got := Evaluate(config.Policy{Mode: config.ModeObserve, UnknownInput: "deny"}, subject(op))
	if got.Decision != protocol.DecisionAudit || !got.WouldDeny {
		t.Fatalf("observe input denial: %+v", got)
	}
	op.Execution = model.ExecutionSkipped
	if got := Evaluate(config.Policy{Mode: config.ModeEnforce, UnknownInput: "deny"}, subject(op)); got.Decision != protocol.DecisionAllow {
		t.Fatalf("skipped input denied: %+v", got)
	}
}

func TestRuleOnUnresolved(t *testing.T) {
	for _, tc := range []struct {
		name, repo, host, unresolved string
		match                        config.Match
		definite                     bool
	}{
		{"missing repository", "", "github.com", "", config.Match{Repositories: config.StringList{"org/*"}}, false},
		{"missing host", "org/repo", "", "", config.Match{Hosts: config.StringList{"github.com"}}, false},
		{"ambiguous candidate", "other/repo", "github.com", "target.repository", config.Match{Repositories: config.StringList{"org/*"}}, false},
		{"ambiguous host", "org/repo", "other.example", "target.host", config.Match{Hosts: config.StringList{"github.com"}}, false},
		{"resolved match", "org/repo", "github.com", "", config.Match{Repositories: config.StringList{"org/*"}}, true},
		{"unrelated unresolved", "org/repo", "github.com", "target.number", config.Match{Repositories: config.StringList{"org/*"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, behavior := range []string{"", "deny", "ignore"} {
				op := operation("pr.merge.direct", tc.repo, model.EffectWrite)
				op.Target.Host = tc.host
				if tc.unresolved != "" {
					op.Unresolved = []model.Unresolved{{Field: tc.unresolved, Reason: "ambiguous"}}
				}
				p := config.Policy{Mode: config.ModeEnforce, Rules: []config.Rule{{ID: "targeted", Decision: "deny", Match: tc.match, OnUnresolved: behavior}}}
				got := Evaluate(p, subject(op))
				wantDeny := tc.definite || behavior != "ignore"
				if got.WouldDeny != wantDeny || (len(got.Rules) == 1) != wantDeny {
					t.Fatalf("%s: %+v", behavior, got)
				}
				if wantDeny && !tc.definite && !strings.Contains(strings.Join(got.DenyReasons, " "), "matched conservatively (target unresolved)") {
					t.Fatalf("missing conservative match evidence: %+v", got)
				}
			}
		})
	}
	// A resolved mismatch on either criterion rules out even a maybe-match.
	op := operation("pr.merge.direct", "", model.EffectWrite)
	op.Target.Host = "other.example"
	p := config.Policy{Mode: config.ModeEnforce, Rules: []config.Rule{{ID: "targeted", Decision: "deny", Match: config.Match{Repositories: config.StringList{"org/*"}, Hosts: config.StringList{"github.com"}}}}}
	if got := Evaluate(p, subject(op)); got.WouldDeny {
		t.Fatalf("definite mismatch matched: %+v", got)
	}
}
