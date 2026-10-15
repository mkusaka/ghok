package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProjectFailureRetainsWholeUserConfig(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"parse", "version: ["},
		{"validate", "version: 1\npolicy: {mode: invalid}\n"},
		{"restriction", "version: 1\nlogging: {}\n"},
		{"collision", "version: 1\npolicy: {mode: enforce, protectedRepositories: [project/repo], rules: [{id: user, decision: audit}]}\n"},
		{"read", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
			repo := filepath.Join(root, "repo")
			writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
			global := DefaultPath(env)
			writeFixture(t, global, "version: 1\npolicy: {mode: observe, rules: [{id: user, decision: deny}]}\nhooks: [{id: user, phase: pre, command: [./user]}]\n")
			want, err := Load(global)
			if err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(repo, ".ghok.yaml")
			if tc.name == "read" {
				if err := os.Mkdir(project, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixture(t, project, tc.body)
			}
			got, notes, err := LoadEffective(global, repo, env)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Policy, want.Policy) || !reflect.DeepEqual(got.Hooks, want.Hooks) || !reflect.DeepEqual(got.Runtime, want.Runtime) || !reflect.DeepEqual(got.Logging, want.Logging) || len(got.IgnoredHooks) != 0 {
				t.Fatalf("project failure changed user config: got=%+v want=%+v", got, want)
			}
			if len(got.Diagnostics) != 1 || !strings.Contains(strings.Join(notes, "\n"), "ignored; user config retained") {
				t.Fatalf("project failure not reported: diagnostics=%v notes=%v", got.Diagnostics, notes)
			}
			env["GHOK_FAIL_CLOSED"] = "1"
			got, _, err = LoadEffective(global, repo, env)
			if !FailClosed(err, env) || !reflect.DeepEqual(got.Policy, want.Policy) {
				t.Fatalf("forced failure must retain user config and return an error: %+v, %v", got, err)
			}
		})
	}
}

func TestProjectUnknownKeysAreAdvisory(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	body := `version: 1
future: true
policy:
  <<: &defaults {futurePolicy: true}
  mode: enforce
  rules:
    - id: known
      decision: deny
      futureRule: true
      match: {command: pr merge, futureMatch: true, env: {ANY_NAME: "*"}}
hooks:
  - id: project
    phase: pre
    command: [./check]
    futureHook: true
    env: {ANY_NAME: value}
trust: {futureTrust: true}
`
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), body)
	c, notes, err := LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Mode != ModeEnforce || len(c.Policy.Rules) != 1 || c.Policy.Rules[0].ID != "known" || len(c.IgnoredHooks) != 1 || len(c.Diagnostics) != 0 {
		t.Fatalf("unknown keys dropped known project settings: %+v", c)
	}
	text := strings.Join(notes, "\n")
	for _, key := range []string{"future", "policy.futurePolicy", "policy.rules[0].futureRule", "policy.rules[0].match.futureMatch", "hooks[0].futureHook", "trust.futureTrust"} {
		if !strings.Contains(text, "unknown key "+key+" ignored") {
			t.Errorf("missing %s: %s", key, text)
		}
	}
	if strings.Contains(text, "unknown key ANY_NAME") {
		t.Fatalf("dynamic map key rejected: %s", text)
	}
	if _, err := Parse([]byte(body), root); err == nil {
		t.Fatal("user config must reject unknown keys")
	}
}

func TestInvalidUserRawMode(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		closed     bool
	}{
		{"strict-field", "version: 1\npolicy: {mode: enforce}\nfuture: true", true},
		{"bad-version", "version: wrong\npolicy: {mode: enforce}", true},
		{"validation", "version: 1\npolicy: {mode: enforce}\nhooks: [{id: bad}]", true},
		{"merge", "version: 1\n<<: {policy: {mode: enforce}}\nfuture: true", true},
		{"alias", "version: 1\nfuture: &mode enforce\npolicy: {mode: *mode}", true},
		{"observe", "version: 1\npolicy: {mode: observe}\nfuture: true", false},
		{"missing-mode", "version: 1\nfuture: true", false},
		{"invalid-mode", "version: 1\npolicy: {mode: [enforce]}", false},
		{"invalid-yaml", "version: 1\npolicy: {mode: enforce}\nbroken: [", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			writeFixture(t, file, tc.body)
			_, err := Load(file)
			if err == nil {
				t.Fatal("invalid user config accepted")
			}
			if got := FailClosed(err, nil); got != tc.closed {
				t.Fatalf("failClosed=%v want=%v: %v", got, tc.closed, err)
			}
			if !FailClosed(err, map[string]string{"GHOK_FAIL_CLOSED": "1"}) {
				t.Fatal("environment override ignored")
			}
		})
	}
}
