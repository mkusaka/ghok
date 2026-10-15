package policy

import (
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestMatchInvocationEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern map[string]string
		env     map[string]string
		want    bool
	}{
		{"no criteria", nil, nil, true},
		{"exact", map[string]string{"WORKER": "1"}, map[string]string{"WORKER": "1"}, true},
		{"exact mismatch", map[string]string{"WORKER": "1"}, map[string]string{"WORKER": "0"}, false},
		{"set", map[string]string{"WORKER": "*"}, map[string]string{"WORKER": "1"}, true},
		{"set with slash", map[string]string{"WORKER": "*"}, map[string]string{"WORKER": "team/worker"}, true},
		{"set rejects absent", map[string]string{"WORKER": "*"}, nil, false},
		{"set rejects empty", map[string]string{"WORKER": "*"}, map[string]string{"WORKER": ""}, false},
		{"unset", map[string]string{"WORKER": ""}, nil, true},
		{"empty", map[string]string{"WORKER": ""}, map[string]string{"WORKER": ""}, true},
		{"unset rejects set", map[string]string{"WORKER": ""}, map[string]string{"WORKER": "1"}, false},
		{"glob", map[string]string{"WORKER": "worker-[12]?"}, map[string]string{"WORKER": "worker-2a"}, true},
		{"case sensitive", map[string]string{"WORKER": "worker-*"}, map[string]string{"WORKER": "Worker-1"}, false},
		{"glob slash boundary", map[string]string{"WORKER": "worker-*"}, map[string]string{"WORKER": "worker-a/b"}, false},
		{"invalid glob", map[string]string{"WORKER": "["}, map[string]string{"WORKER": "1"}, false},
		{"all entries", map[string]string{"WORKER": "1", "CI": "true"}, map[string]string{"WORKER": "1", "CI": "true"}, true},
		{"one entry fails", map[string]string{"WORKER": "1", "CI": "true"}, map[string]string{"WORKER": "1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := subject(operation("pr.merge.direct", "org/repo", model.EffectWrite))
			s.Env = tc.env
			m := config.Match{Env: tc.pattern, Operations: config.StringList{"pr.merge.*"}}
			if got, _ := Matches(m, s); got != tc.want {
				t.Fatalf("Matches=%t, want %t", got, tc.want)
			}
			// Unknown targets must not bypass invocation-level criteria.
			m.Repositories = config.StringList{"org/*"}
			s.Result.Operations[0].Target.Repository = ""
			if got, _ := MatchMaybe(m, s); got != tc.want {
				t.Fatalf("MatchMaybe=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestEnvironmentConstrainedRule(t *testing.T) {
	p := config.Policy{Mode: config.ModeEnforce, Rules: []config.Rule{{
		ID: "no-worker-merges", Decision: "deny",
		Match: config.Match{Env: map[string]string{"MALL_GH_WORKER": "1"}, Operations: config.StringList{"pr.merge.*"}},
	}}}
	for _, tc := range []struct {
		worker, action string
		want           protocol.Decision
	}{
		{"1", "pr.merge.direct", protocol.DecisionDeny},
		{"", "pr.merge.direct", protocol.DecisionAllow},
		{"1", "pr.review.approve", protocol.DecisionAllow},
	} {
		s := subject(operation(tc.action, "org/repo", model.EffectWrite))
		s.Env = map[string]string{"MALL_GH_WORKER": tc.worker}
		if got := Evaluate(p, s); got.Decision != tc.want {
			t.Fatalf("worker=%q action=%s: decision=%s, want %s", tc.worker, tc.action, got.Decision, tc.want)
		}
	}
}
