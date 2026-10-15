package config

import (
	"strings"
	"testing"
)

func TestValidateMatchEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, value, problem string
	}{
		{"WORKER", "1", ""},
		{"_worker2", "*", ""},
		{"Empty", "", ""},
		{"GLOB", "worker-[12]?", ""},
		{"", "1", "invalid environment variable name"},
		{"1WORKER", "1", "invalid environment variable name"},
		{"BAD-NAME", "1", "invalid environment variable name"},
		{"BAD=NAME", "1", "invalid environment variable name"},
		{"BAD\x00NAME", "1", "invalid environment variable name"},
		{"WORKER", "[", "invalid glob"},
	} {
		for _, location := range []string{"rule", PhasePre, PhasePost} {
			t.Run(location+"/"+tc.name+"/"+tc.value, func(t *testing.T) {
				c := validConfig()
				m := Match{Env: map[string]string{tc.name: tc.value}}
				key := "policy.rules[0].match.env["
				switch location {
				case "rule":
					c.Policy.Rules = []Rule{{ID: "worker", Decision: "deny", Match: m}}
				case PhasePre:
					c.Hooks[0].Match = m
					key = "hooks[0].match.env["
				case PhasePost:
					c.Hooks[1].Match = m
					key = "hooks[1].match.env["
				}
				err := c.Validate()
				if tc.problem == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), tc.problem) {
					t.Fatalf("wanted %s at %s, got %v", tc.problem, key, err)
				}
			})
		}
	}
}
