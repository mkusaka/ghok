package policy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func operation(action, repo string, effect model.Effect) model.Operation {
	return model.Operation{Family: "pr.merge", Action: action, Effect: effect, Execution: model.ExecutionWillRun, Target: model.Target{Repository: repo, Host: "GitHub.COM"}}
}
func subject(ops ...model.Operation) Subject {
	return Subject{Result: model.Result{Classification: model.Classification{Level: model.LevelL3}, Operations: ops}, Argv: []string{"api", "graphql"}, Phase: "pre"}
}

func TestGlobSemantics(t *testing.T) {
	for _, c := range []struct {
		pattern, value string
		want           bool
	}{
		{"*", "pr.merge.direct", true}, {"pr.merge.*", "pr.merge.auto.enable", true}, {"pr.merge", "pr.merge.direct", false},
		{"pr.merge", "pr.merge", true}, {"pr.*.direct", "pr.merge.direct", true}, {"pr.merge.*", "pr.review.approve", false},
		{"[", "pr.merge", false}, {"pr.merge.?", "pr.merge.a", true},
	} {
		if got := MatchAction(c.pattern, c.value); got != c.want {
			t.Errorf("MatchAction(%q,%q)=%v", c.pattern, c.value, got)
		}
	}
	for _, c := range []struct {
		pattern, value string
		want           bool
	}{
		{"ORG/*", "org/Repo", true}, {"org/repo", "ORG/REPO", true}, {"org/*", "org/x/y", false}, {"[", "org/repo", false}, {"org/r", "org/repo", false},
	} {
		if got := MatchRepo(c.pattern, c.value); got != c.want {
			t.Errorf("MatchRepo(%q,%q)=%v", c.pattern, c.value, got)
		}
	}
}

func TestMatchingCriteria(t *testing.T) {
	op := operation("pr.merge.direct", "org/repo", model.EffectWrite)
	s := subject(op)
	for _, c := range []struct {
		name  string
		match config.Match
		want  bool
	}{
		{"empty", config.Match{}, true},
		{"action OR", config.Match{Operations: config.StringList{"pr.review.*", "pr.merge.*"}}, true},
		{"action mismatch", config.Match{Operations: config.StringList{"pr.review.*"}}, false},
		{"repo case", config.Match{Repositories: config.StringList{"ORG/*"}}, true},
		{"repo mismatch", config.Match{Repositories: config.StringList{"other/*"}}, false},
		{"host case", config.Match{Hosts: config.StringList{"GITHUB.com"}}, true},
		{"host mismatch", config.Match{Hosts: config.StringList{"ghe.example"}}, false},
		{"effect", config.Match{Effects: config.StringList{"read", "write"}}, true},
		{"effect mismatch", config.Match{Effects: config.StringList{"read"}}, false},
		{"level", config.Match{Levels: config.StringList{"L2", "L3"}}, true},
		{"level mismatch", config.Match{Levels: config.StringList{"L1"}}, false},
		{"command prefix", config.Match{Commands: config.StringList{"api"}}, true},
		{"command path", config.Match{Commands: config.StringList{"api graphql"}}, true},
		{"command boundary", config.Match{Commands: config.StringList{"ap"}}, false},
		{"command too long", config.Match{Commands: config.StringList{"api graphql query"}}, false},
		{"empty command", config.Match{Commands: config.StringList{" "}}, false},
		{"AND", config.Match{Operations: config.StringList{"pr.merge.*"}, Effects: config.StringList{"read"}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, indices := Matches(c.match, s)
			if got != c.want || got && !reflect.DeepEqual(indices, []int{0}) {
				t.Fatalf("got %v %v", got, indices)
			}
		})
	}
	s.Result.Operations = append(s.Result.Operations, operation("pr.review.approve", "other/repo", model.EffectWrite))
	if yes, _ := Matches(config.Match{Operations: config.StringList{"pr.merge.*"}, Repositories: config.StringList{"other/*"}}, s); yes {
		t.Fatal("criteria matched across different operations")
	}
	s.Argv = []string{"-R", "org/repo", "pr", "merge"}
	s.Result.Operations[0].Origin.Command = []string{"pr", "merge"}
	if yes, _ := Matches(config.Match{Commands: config.StringList{"pr merge"}}, s); !yes {
		t.Fatal("normalized command origin not used")
	}
}

func TestUnresolvedMatchingAndExecution(t *testing.T) {
	m := config.Match{Repositories: config.StringList{"org/*"}, Hosts: config.StringList{"github.com"}}
	for _, execution := range []model.Execution{model.ExecutionWillRun, model.ExecutionMaybe, "", model.ExecutionSkipped, model.ExecutionNotSelected} {
		op := operation("pr.merge.direct", "", model.EffectWrite)
		op.Execution = execution
		s := subject(op)
		if yes, _ := Matches(m, s); yes {
			t.Fatal("unresolved target asserted definite")
		}
		maybe, indices := MatchMaybe(m, s)
		want := execution != model.ExecutionSkipped && execution != model.ExecutionNotSelected
		if maybe != want || maybe && !reflect.DeepEqual(indices, []int{0}) {
			t.Fatalf("%s: %v %v", execution, maybe, indices)
		}
	}
	op := operation("pr.merge.direct", "", model.EffectWrite)
	s := subject(op)
	s.Context = &model.ResolvedContext{Repository: "org/repo"}
	if yes, _ := Matches(m, s); !yes {
		t.Fatal("implicit target did not use resolved context")
	}
	s.Result.Operations[0].Target.NodeID = "PR_node"
	if yes, _ := MatchMaybe(m, s); !yes {
		t.Fatal("node target borrowed current repository")
	}
	s.Result.Operations[0].Target.NodeID = ""
	s.Result.Operations[0].Unresolved = []model.Unresolved{{Field: "target.repository", Reason: "variable unknown"}}
	if yes, _ := MatchMaybe(m, s); !yes {
		t.Fatal("explicit unresolved target borrowed current repository")
	}
	s.Result.Operations[0].Target.Host = "ghe.example"
	if yes, _ := MatchMaybe(m, s); yes {
		t.Fatal("known host mismatch cannot maybe match")
	}
	if yes, _ := Matches(config.Match{}, Subject{}); !yes {
		t.Fatal("invocation-only selection with no operations")
	}
	if yes, _ := Matches(config.Match{Effects: config.StringList{"write"}}, Subject{}); yes {
		t.Fatal("no operations matched write")
	}
}

func TestEvaluationUnknownProtectedModes(t *testing.T) {
	known := operation("pr.merge.direct", "org/repo", model.EffectWrite)
	unknown := operation("unknown", "org/repo", model.EffectUnknown)
	unknown.Family = model.FamilyUnknown
	unknown.UnknownReason = model.UnknownMutation
	unknownWrite := unknown
	unknownWrite.Effect = model.EffectWrite
	unknownRead := unknown
	unknownRead.Effect = model.EffectRead
	unresolved := known
	unresolved.Target.Repository = ""
	knownUnknown := known
	knownUnknown.Effect = model.EffectUnknown
	protected := []string{"ORG/*"}
	for _, c := range []struct {
		name     string
		p        config.Policy
		op       model.Operation
		decision protocol.Decision
		deny     bool
	}{
		{"default observe", config.Policy{}, unknown, protocol.DecisionAudit, false},
		{"unknown audit enforce", config.Policy{Mode: "enforce"}, unknown, protocol.DecisionAudit, false},
		{"unknown write deny", config.Policy{Mode: "enforce", UnknownMutation: "deny"}, unknownWrite, protocol.DecisionDeny, true},
		{"unknown allow", config.Policy{Mode: "enforce", UnknownMutation: "allow"}, unknown, protocol.DecisionAllow, false},
		{"unknown read safe", config.Policy{Mode: "enforce", UnknownMutation: "deny"}, unknownRead, protocol.DecisionAllow, false},
		{"known unknown effect", config.Policy{Mode: "enforce", UnknownMutation: "deny"}, knownUnknown, protocol.DecisionDeny, true},
		{"protected default", config.Policy{Mode: "enforce", UnknownMutation: "allow", ProtectedRepositories: protected}, unknown, protocol.DecisionDeny, true},
		{"protected allow", config.Policy{Mode: "enforce", UnknownMutation: "allow", ProtectedRepositories: protected, ProtectedUnknown: "allow"}, unknown, protocol.DecisionAllow, false},
		{"protected audit", config.Policy{Mode: "enforce", UnknownMutation: "allow", ProtectedRepositories: protected, ProtectedUnknown: "audit"}, unknown, protocol.DecisionAudit, false},
		{"known protected write", config.Policy{Mode: "enforce", ProtectedRepositories: protected}, known, protocol.DecisionAllow, false},
		{"known unresolved protected", config.Policy{Mode: "enforce", ProtectedRepositories: protected}, unresolved, protocol.DecisionDeny, true},
		{"unresolved no protected", config.Policy{Mode: "enforce"}, unresolved, protocol.DecisionAllow, false},
		{"observe would deny", config.Policy{UnknownMutation: "deny"}, unknown, protocol.DecisionAudit, true},
		{"protected read", config.Policy{Mode: "enforce", ProtectedRepositories: protected, UnknownMutation: "deny"}, unknownRead, protocol.DecisionAllow, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.p, subject(c.op))
			if got.Decision != c.decision || got.WouldDeny != c.deny {
				t.Fatalf("%+v", got)
			}
		})
	}
	outside := unknown
	outside.Target.Repository = "other/repo"
	if got := Evaluate(config.Policy{Mode: "enforce", UnknownMutation: "allow", ProtectedRepositories: protected}, subject(outside)); got.Decision != protocol.DecisionAllow {
		t.Fatalf("outside protected: %+v", got)
	}
	zero := known
	zero.Effect = ""
	if got := Evaluate(config.Policy{Mode: "enforce", UnknownMutation: "deny"}, subject(zero)); !got.WouldDeny {
		t.Fatal("empty effect treated as safe")
	}
}

func TestRulesAggregationAndEvidence(t *testing.T) {
	ops := []model.Operation{operation("pr.merge.direct", "org/repo", model.EffectWrite), operation("pr.review.approve", "org/repo", model.EffectWrite)}
	p := config.Policy{Mode: "enforce", Rules: []config.Rule{
		{ID: "audit", Match: config.Match{Operations: config.StringList{"pr.*"}}, Decision: "audit", Reason: "record"},
		{ID: "deny", Match: config.Match{Operations: config.StringList{"pr.review.*"}}, Decision: "deny", Reason: "blocked"},
		{ID: "allow", Decision: "allow", Reason: "cannot override"},
	}}
	got := Evaluate(p, subject(ops...))
	if got.Decision != protocol.DecisionDeny || !got.WouldDeny || !reflect.DeepEqual(got.Rules, []string{"audit", "deny", "allow"}) || got.PerOp[0].Decision != protocol.DecisionAudit || got.PerOp[1].Decision != protocol.DecisionDeny {
		t.Fatalf("%+v", got)
	}
	ops[1].Execution = model.ExecutionSkipped
	ops[1].Evidence = "@skip(if:true)"
	got = Evaluate(p, subject(ops...))
	if got.WouldDeny || len(got.PerOp) != 1 || !strings.Contains(strings.Join(got.Reasons, " "), "@skip(if:true)") {
		t.Fatalf("%+v", got)
	}
	ops[1].Execution = model.ExecutionNotSelected
	got = Evaluate(p, subject(ops...))
	if got.WouldDeny {
		t.Fatalf("%+v", got)
	}
	ops[1].Execution = model.ExecutionMaybe
	if got = Evaluate(p, subject(ops...)); !got.WouldDeny {
		t.Fatalf("%+v", got)
	}
	ops[0].Target.Repository = ""
	ops[0].Unresolved = []model.Unresolved{{Field: "target.repository", Reason: "node unresolved"}}
	p.Rules = []config.Rule{{ID: "maybe", Match: config.Match{Repositories: config.StringList{"org/*"}}, Decision: "deny", Reason: "protected"}}
	got = Evaluate(p, subject(ops[0]))
	if !got.WouldDeny {
		t.Fatalf("%+v", got)
	}
	p.Rules = nil
	p.ProtectedRepositories = []string{"org/*"}
	got = Evaluate(p, subject(ops[0]))
	if !strings.Contains(strings.Join(got.Reasons, " "), "target repository unresolved; cannot prove it is not protected") {
		t.Fatalf("%+v", got)
	}
	s := subject(operation("query", "", model.EffectRead))
	s.Result.Classification.MutationFree = true
	if got = Evaluate(config.Policy{}, s); !strings.Contains(strings.Join(got.Reasons, " "), "proved mutation-free") || got.Decision != protocol.DecisionAllow {
		t.Fatalf("%+v", got)
	}
	s = subject(model.NewUnknown(model.LevelL3, model.UnknownParseError, "bad JSON").Operations...)
	if got = Evaluate(config.Policy{}, s); !strings.Contains(strings.Join(got.Reasons, " "), "parse-error") || !strings.Contains(strings.Join(got.Reasons, " "), "did not prove mutation-free") {
		t.Fatalf("%+v", got)
	}
}

func TestDenyReasons(t *testing.T) {
	known := operation("pr.merge.direct", "org/repo", model.EffectWrite)
	known.Unresolved = []model.Unresolved{{Field: "target.number", Reason: "informational unresolved selector"}}
	unknown := model.NewUnknown(model.LevelL3, model.UnknownMutation, "unrecognized mutation").Operations[0]
	unknown.Target.Repository = "org/repo"
	unresolved := known
	unresolved.Target.Repository = ""
	rules := []config.Rule{
		{ID: "audit", Decision: "audit", Reason: "audit-only diagnostic"},
		{ID: "no-admin", Decision: "deny", Reason: "direct merges are forbidden"},
	}
	combined := config.Policy{Mode: config.ModeEnforce, Rules: rules, UnknownMutation: config.UnknownDeny, ProtectedRepositories: []string{"org/*"}}
	observe := combined
	observe.Mode = config.ModeObserve
	for _, tc := range []struct {
		name    string
		policy  config.Policy
		subject Subject
		want    []string
	}{
		{"rule", config.Policy{Mode: config.ModeEnforce, Rules: rules}, subject(known),
			[]string{"rule no-admin: direct merges are forbidden (pr.merge.direct)"}},
		{"unknown mutation", config.Policy{Mode: config.ModeEnforce, UnknownMutation: config.UnknownDeny}, subject(unknown),
			[]string{"unclassified-write: unknownMutation policy (unknown)"}},
		{"unresolved protected", config.Policy{Mode: config.ModeEnforce, ProtectedRepositories: []string{"org/*"}}, subject(unresolved),
			[]string{"target repository unresolved; cannot prove it is not protected (pr.merge.direct)"}},
		{"unknown protected", config.Policy{Mode: config.ModeEnforce, UnknownMutation: config.UnknownAllow, ProtectedRepositories: []string{"org/*"}}, subject(unknown),
			[]string{"unclassified-write targets protected repository org/repo (unknown)"}},
		{"combined", combined, subject(unknown),
			[]string{"rule no-admin: direct merges are forbidden (unknown)", "unclassified-write: unknownMutation policy (unknown)", "unclassified-write targets protected repository org/repo (unknown)"}},
		{"observe retains would-deny reasons", observe, subject(unknown),
			[]string{"rule no-admin: direct merges are forbidden (unknown)", "unclassified-write: unknownMutation policy (unknown)", "unclassified-write targets protected repository org/repo (unknown)"}},
		{"invocation-only rule", config.Policy{Mode: config.ModeEnforce, Rules: rules}, subject(),
			[]string{"rule no-admin: direct merges are forbidden"}},
		{"multiple operations", config.Policy{Mode: config.ModeEnforce, Rules: rules}, subject(known, operation("pr.review.approve", "org/repo", model.EffectWrite)),
			[]string{"rule no-admin: direct merges are forbidden (pr.merge.direct)", "rule no-admin: direct merges are forbidden (pr.review.approve)"}},
		{"audit is not deny", config.Policy{Mode: config.ModeEnforce, UnknownMutation: config.UnknownAudit, ProtectedUnknown: config.UnknownAudit, ProtectedRepositories: []string{"org/*"}, Rules: rules[:1]}, subject(unknown), nil},
		{"allow is not deny", config.Policy{Mode: config.ModeEnforce, UnknownMutation: config.UnknownAllow, ProtectedUnknown: config.UnknownAllow, ProtectedRepositories: []string{"org/*"}}, subject(unknown), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.policy, tc.subject)
			if !reflect.DeepEqual(got.DenyReasons, tc.want) {
				t.Fatalf("deny reasons=%q, want=%q", got.DenyReasons, tc.want)
			}
			if got.WouldDeny != (len(tc.want) > 0) {
				t.Fatalf("wouldDeny=%t, deny reasons=%q", got.WouldDeny, got.DenyReasons)
			}
			for _, op := range tc.subject.Result.Operations {
				for _, unresolved := range op.Unresolved {
					if !strings.Contains(strings.Join(got.Reasons, " "), unresolved.Reason) {
						t.Fatal("deny-only reasons replaced unresolved diagnostics")
					}
				}
			}
			if tc.policy.Mode == config.ModeObserve && got.Decision == protocol.DecisionDeny {
				t.Fatal("observe enforced a would-be denial")
			}
		})
	}
	for _, execution := range []model.Execution{model.ExecutionSkipped, model.ExecutionNotSelected} {
		skipped := unknown
		skipped.Execution = execution
		if got := Evaluate(combined, subject(skipped)); len(got.DenyReasons) != 0 {
			t.Fatalf("%s operation produced deny reasons: %q", execution, got.DenyReasons)
		}
	}
}

func TestOutcomeMatches(t *testing.T) {
	ok := &protocol.ExecResult{ExitCode: 0}
	failed := &protocol.ExecResult{ExitCode: 2}
	signaled := &protocol.ExecResult{ExitCode: -1, Signal: "SIGINT"}
	notStarted := &protocol.ExecResult{ExitCode: 127, StartError: "not found"}
	for _, tc := range []struct {
		m    config.Match
		o    *protocol.ExecResult
		want bool
	}{
		{config.Match{}, failed, true},
		{config.Match{Results: config.StringList{"failure"}}, nil, true}, // unknown yet: candidate
		{config.Match{Results: config.StringList{"failure"}}, ok, false},
		{config.Match{Results: config.StringList{"failure"}}, failed, true},
		{config.Match{Results: config.StringList{"failure"}}, signaled, true},
		{config.Match{Results: config.StringList{"failure"}}, notStarted, true},
		{config.Match{Results: config.StringList{"success"}}, ok, true},
		{config.Match{Results: config.StringList{"success", "failure"}}, failed, true},
		{config.Match{ExitCodes: []int{2, 3}}, failed, true},
		{config.Match{ExitCodes: []int{0}}, failed, false},
		{config.Match{ExitCodes: []int{127}}, notStarted, false}, // start failure is not an exit code
		{config.Match{Results: config.StringList{"failure"}, ExitCodes: []int{3}}, failed, false},
	} {
		if got := outcomeMatches(tc.m, tc.o); got != tc.want {
			t.Errorf("match=%+v outcome=%+v: got %v", tc.m, tc.o, got)
		}
	}
}
