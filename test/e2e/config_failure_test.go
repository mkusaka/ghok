//go:build unix

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
)

func TestProjectFailurePreservesUserEnforcement(t *testing.T) {
	for _, project := range []struct {
		name, body, note string
	}{
		{"junk", "version: [", "ignored"},
		{"invalid", "version: 1\npolicy: {mode: broken}\n", "ignored"},
		{"unknown", "version: 1\nfuture: true\npolicy: {futurePolicy: true, rules: [{id: project, decision: audit, futureRule: true, match: {futureMatch: true}}]}\nhooks: {pre: [{id: project-hook, command: [./check], futureHook: true}]}\ntrust: {futureTrust: true}\n", "unknown"},
		{"collision", "version: 1\npolicy: {rules: [{id: user-deny, decision: audit}]}\n", "ignored"},
	} {
		t.Run(project.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				c.Policy.Rules = []config.Rule{{ID: "user-deny", Decision: "deny", Match: config.Match{Commands: []string{"pr merge"}}}}
				c.Hooks = []config.Hook{s.hook("user", "pre")}
				yes := true
				c.Logging.Enabled = &yes
				c.Logging.Level = "decision"
			})
			if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.cwd, ".ghok.yaml"), []byte(project.body), 0600); err != nil {
				t.Fatal(err)
			}
			result := s.gh(nil, "pr", "merge", "12", "-R", "acme/repo")
			result.code(t, 77)
			if len(s.records()) != 0 || !bytes.Contains(result.stderr, []byte("denied by rule user-deny")) {
				t.Fatalf("project disabled user policy: records=%+v stderr=%s", s.records(), result.stderr)
			}
			s.hookInput("user")
			logs := s.logs()
			if project.note == "ignored" && (len(logs) != 1 || !strings.Contains(strings.Join(logs[0].Diagnostics, "\n"), "project config")) {
				t.Fatalf("project failure not logged: %+v", logs)
			}
			if project.note == "ignored" {
				s.gh(nil, "version").code(t, 0)
				logs = s.logs()
				if len(logs) != 2 || logs[1].Decision != "allow" || !strings.Contains(strings.Join(logs[1].Diagnostics, "\n"), "project config") {
					t.Fatalf("decision logging lost project failure on allowed gh: %+v", logs)
				}
			}
			for _, args := range [][]string{{"config", "validate"}, {"explain", "--", "version"}, {"doctor", "--json"}} {
				result = s.management(nil, args...)
				if args[0] != "doctor" {
					result.code(t, 0)
				}
				if !bytes.Contains(result.stdout, []byte(project.note)) || !bytes.Contains(result.stdout, []byte(".ghok.yaml")) {
					t.Fatalf("missing project note for %v: %s %s", args, result.stdout, result.stderr)
				}
			}
		})
	}
}

func TestInvalidUserConfigFailureMode(t *testing.T) {
	for _, tc := range []struct {
		name, body, force string
		closed            bool
	}{
		{"enforce", "version: 1\npolicy: {mode: enforce}\nunknown: true\n", "", true},
		{"observe", "version: 1\npolicy: {mode: observe}\nunknown: true\n", "", false},
		{"undetermined", "version: [", "", false},
		{"forced-observe", "version: 1\npolicy: {mode: observe}\nunknown: true\n", "1", true},
		{"forced-unreadable", "", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.env["GHOK_FAIL_CLOSED"] = tc.force
			if tc.name == "forced-unreadable" {
				if err := os.Remove(s.cfg); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(s.cfg, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(s.cfg, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			behavior := "fail-open"
			wantCode := 0
			if tc.closed {
				behavior, wantCode = "fail-closed", 78
			}
			for run := range 2 {
				result := s.gh(nil, "version")
				result.code(t, wantCode)
				if tc.closed {
					if len(s.records()) != 0 || !bytes.Contains(result.stderr, []byte("not running gh")) {
						t.Fatalf("fail-closed not enforced every time: %s", result.stderr)
					}
				} else if run == 0 {
					if !bytes.Contains(result.stderr, []byte("running gh without hooks")) {
						t.Fatalf("missing fail-open warning: %s", result.stderr)
					}
				} else if len(result.stderr) != 0 {
					t.Fatalf("fail-open warning not throttled: %s", result.stderr)
				}
			}
			if !tc.closed && len(s.records()) != 2 {
				t.Fatalf("passthrough executions=%d", len(s.records()))
			}
			result := s.management(nil, "config", "validate")
			result.code(t, 1)
			if !bytes.Contains(result.stdout, []byte(behavior)) {
				t.Fatalf("validate omitted %s: %s", behavior, result.stdout)
			}
			result = s.management(nil, "doctor", "--json")
			result.code(t, 1)
			var checks []install.Check
			decodeJSON(t, result.stdout, &checks)
			found := false
			for _, check := range checks {
				found = found || check.Name == "config" && strings.Contains(check.Detail, behavior)
			}
			if !found {
				t.Fatalf("doctor omitted %s: %+v", behavior, checks)
			}
		})
	}
}

func TestHomeDotfilesDoNotBecomeProject(t *testing.T) {
	s := newSandbox(t)
	home := s.env["HOME"]
	if err := os.Mkdir(filepath.Join(home, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ghok.yaml"), []byte("version: 1\npolicy: {mode: enforce, rules: [{id: home-deny, decision: deny}]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.cwd = filepath.Join(home, "child")
	if err := os.Mkdir(s.cwd, 0700); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{s.cwd, home} {
		s.cwd = cwd
		s.gh(nil, "version").code(t, 0)
	}
	if len(s.records()) != 2 {
		t.Fatalf("home config affected passthrough: %+v", s.records())
	}
}

func TestFailClosedEnvironmentRejectsInvalidProject(t *testing.T) {
	s := newSandbox(t)
	s.env["GHOK_FAIL_CLOSED"] = "1"
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cwd, ".ghok.yaml"), []byte("version: ["), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result := s.gh(nil, "version")
		result.code(t, 78)
		if !bytes.Contains(result.stderr, []byte("not running gh")) || len(s.records()) != 0 {
			t.Fatalf("CI override allowed invalid project: %s records=%+v", result.stderr, s.records())
		}
	}
}
