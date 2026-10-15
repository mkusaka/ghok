//go:build unix

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"go.yaml.in/yaml/v3"
)

func writePerHookProject(t *testing.T, s *sandbox, gate bool) string {
	t.Helper()
	path, _ := warningProject(t, s, func(c *config.Config) {
		c.Hooks = c.Hooks[:2]
		for i := range c.Hooks {
			c.Hooks[i].Match.Commands = []string{"pr view"}
			c.Hooks[i].Files = []string{c.Hooks[i].ID + ".txt"}
		}
		if gate {
			c.Hooks[0].OnError = config.OnErrorDeny
		}
	})
	for _, name := range []string{"first.txt", "second.txt"} {
		if err := os.WriteFile(filepath.Join(s.cwd, name), []byte("reviewed\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestSelectiveTrustPreservesUnchangedSibling(t *testing.T) {
	s := newSandbox(t)
	project := writePerHookProject(t, s, false)
	s.management(nil, "config", "trust", "--hook", "first", "--hook", "second").code(t, 0)
	if err := os.WriteFile(filepath.Join(s.cwd, "first.txt"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.gh(nil, "pr", "view", "1", "-R", "acme/repo").code(t, 0)
	s.hookInput("second")
	if _, err := os.Stat(filepath.Join(s.root, "first.json")); !os.IsNotExist(err) {
		t.Fatalf("changed hook executed: %v", err)
	}
	list := s.management(nil, "hooks", "list", "--json")
	list.code(t, 0)
	var rows []struct{ ID, Ignored string }
	decodeJSON(t, list.stdout, &rows)
	ignored := map[string]string{}
	for _, row := range rows {
		ignored[row.ID] = row.Ignored
	}
	if ignored["first"] != "changed since trust" || ignored["second"] != "" {
		t.Fatalf("partial trust=%+v", rows)
	}
	// Re-approving an unchanged sibling must not approve the changed hook.
	s.management(nil, "config", "trust", "--hook", "second", project).code(t, 0)
	s.gh(nil, "pr", "view", "1", "-R", "acme/repo").code(t, 0)
	if _, err := os.Stat(filepath.Join(s.root, "first.json")); !os.IsNotExist(err) {
		t.Fatalf("selective trust approved changed sibling: %v", err)
	}
	s.management(nil, "config", "trust", "--hook", "first").code(t, 0)
	s.gh(nil, "pr", "view", "1", "-R", "acme/repo").code(t, 0)
	s.hookInput("first")
	s.hookInput("second")
	s.management(nil, "config", "trust", "--hook", "missing").code(t, 1)
	s.management(nil, "config", "trust", "--list", "--hook", "first").code(t, 2)
}

func TestChangedGateEnforceAndObserve(t *testing.T) {
	for _, mode := range []string{config.ModeEnforce, config.ModeObserve} {
		t.Run(mode, func(t *testing.T) {
			s := newSandbox(t)
			logPath := filepath.Join(s.root, "decisions.jsonl")
			s.configure(func(c *config.Config) {
				c.Policy.Mode = mode
				yes := true
				c.Logging.Enabled = &yes
				c.Logging.Path = logPath
			})
			writePerHookProject(t, s, true)
			args := []string{"pr", "view", "1", "-R", "acme/repo"}
			// A never-trusted project remains inactive without introducing denial.
			s.gh(nil, args...).code(t, 0)
			s.management(nil, "config", "trust").code(t, 0)
			if err := os.WriteFile(filepath.Join(s.cwd, "first.txt"), []byte("changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := len(s.records())
			for range 2 {
				result := s.gh(nil, args...)
				if mode == config.ModeEnforce {
					result.code(t, 78)
					for _, want := range []string{"denied by config first", "hook first is not trusted (changed since trust); run 'ghok config trust'"} {
						if !bytes.Contains(result.stderr, []byte(want)) {
							t.Fatalf("missing gate denial %q: %s", want, result.stderr)
						}
					}
				} else {
					result.code(t, 0)
				}
			}
			wantRecords := before
			if mode == config.ModeObserve {
				wantRecords += 2
			}
			if got := len(s.records()); got != wantRecords {
				t.Fatalf("gate invocation count=%d want=%d", got, wantRecords)
			}
			if _, err := os.Stat(filepath.Join(s.root, "first.json")); !os.IsNotExist(err) {
				t.Fatalf("inactive gate executed: %v", err)
			}
			for _, command := range [][]string{{"explain", "--", "pr", "view", "1", "-R", "acme/repo"}, {"hooks", "test", "--json", "--", "pr", "view", "1", "-R", "acme/repo"}} {
				result := s.management(nil, command...)
				wantCode := 0
				if command[0] == "hooks" && mode == config.ModeEnforce {
					wantCode = 1
				}
				result.code(t, wantCode)
				if !bytes.Contains(result.stdout, []byte("hook first is not trusted")) {
					t.Fatalf("analysis omitted inactive gate: %s", result.stdout)
				}
			}
			if mode == config.ModeObserve {
				data, err := os.ReadFile(logPath)
				if err != nil || !bytes.Contains(data, []byte(`"decision":"would-deny"`)) || !bytes.Contains(data, []byte("hook first is not trusted")) {
					t.Fatalf("observe gate log=%s err=%v", data, err)
				}
			}
			// A constraint mismatch never activates the inactive gate.
			s.gh(nil, "pr", "list", "-R", "acme/repo").code(t, 0)
		})
	}
}

func TestUserHookFilesRejected(t *testing.T) {
	s := newSandbox(t)
	c := config.Config{Version: 1, Hooks: []config.Hook{s.hook("user", "pre")}}
	c.Hooks[0].Files = []string{"dependency.txt"}
	c.ApplyDefaults()
	body, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.cfg, body, 0600); err != nil {
		t.Fatal(err)
	}
	result := s.management(nil, "config", "validate")
	result.code(t, 1)
	if !bytes.Contains(result.stderr, []byte("files")) {
		t.Fatalf("missing user hook files error: %s", result.stderr)
	}
}

func TestSelectiveTrustLeavesUnapprovedGateBlocked(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) { c.Policy.Mode = config.ModeEnforce })
	writePerHookProject(t, s, true)
	s.management(nil, "config", "trust", "--hook", "second").code(t, 0)
	result := s.gh(nil, "pr", "view", "1", "-R", "acme/repo")
	result.code(t, 78)
	if !bytes.Contains(result.stderr, []byte("denied by config first")) {
		t.Fatalf("unapproved gate missing config denial: %s", result.stderr)
	}
	s.absentRecord()
	s.management(nil, "config", "trust", "--hook", "first").code(t, 0)
	s.gh(nil, "pr", "view", "1", "-R", "acme/repo").code(t, 0)
	s.hookInput("first")
	s.hookInput("second")
}

func TestHookDependingOnIgnoredHookSkips(t *testing.T) {
	s := newSandbox(t)
	project, _ := warningProject(t, s, func(c *config.Config) {
		c.Hooks = c.Hooks[:2]
		c.Hooks[0].Files = []string{"first.txt"}
		c.Hooks[1].DependsOn = []string{"first"}
	})
	dependency := filepath.Join(s.cwd, "first.txt")
	if err := os.WriteFile(dependency, []byte("reviewed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust", project).code(t, 0)
	if err := os.WriteFile(dependency, []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := s.management(nil, "hooks", "test", "--json", "--", "version")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("dependency")) || !bytes.Contains(result.stdout, []byte("skipped")) {
		t.Fatalf("dependent hook not skipped: %s", result.stdout)
	}
	for _, name := range []string{"first", "second"} {
		if _, err := os.Stat(filepath.Join(s.root, name+".json")); !os.IsNotExist(err) {
			t.Fatalf("hook %s executed: %v", name, err)
		}
	}
}
