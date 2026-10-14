package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsAndVersions(t *testing.T) {
	for _, text := range []string{"", "# empty\n", "{}", "version: 1\n"} {
		c, err := Parse([]byte(text), "/config")
		if err != nil {
			t.Fatal(err)
		}
		if c.Version != 1 || c.Policy.Mode != ModeObserve || c.Policy.UnknownMutation != UnknownAudit || c.Policy.ProtectedUnknown != UnknownDeny || c.Runtime.HookConcurrency != 1 || c.Runtime.StdinMaxBytes != 8<<20 || c.Runtime.StdinTimeout.D() != DefaultStdinTimeout || c.Runtime.FileMaxBytes != 8<<20 || c.Runtime.CaptureLimit != 1<<20 || c.Runtime.ResolveTimeout.D() != DefaultResolveTimeout || !*c.Logging.Enabled || c.Logging.Level != "info" || c.Logging.Path != "/config/logs/ghok.jsonl" {
			t.Fatalf("defaults: %+v", c)
		}
	}
	for _, text := range []string{"policy: {mode: enforce}", "version: 0", "version: 2", "version: 1\n---\nversion: 1", "[1, 2]"} {
		if _, err := Parse([]byte(text), ""); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || !filepath.IsAbs(c.Dir) {
		t.Fatalf("missing config: %v, %+v", err, c)
	}
}

func TestSpecExampleAndCommandPaths(t *testing.T) {
	text := `version: 1
policy:
  mode: enforce
  unknownMutation: deny
hooks:
  - id: check-pr-policy
    phase: pre
    match:
      operation: "pr.merge.*"
      repositories: ["org/repo"]
    command: ["./hooks/check-pr-policy"]
    timeout: 10s
    onError: deny
  - id: audit-merge
    phase: post
    match: { operation: "pr.merge.*" }
    command: ["./hooks/audit-merge"]
    timeout: 5s
    onError: ignore
`
	c, err := Parse([]byte(text), "/config")
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Mode != ModeEnforce || c.HookCommandPath(c.Hooks[0]) != "/config/hooks/check-pr-policy" || c.Hooks[1].Timeout.D().String() != "5s" || !*c.Hooks[0].Enabled {
		t.Fatalf("example: %+v", c)
	}
	for _, command := range []string{"hook", "/bin/hook", "hooks/hook", "./hook"} {
		want := command
		if !filepath.IsAbs(command) && strings.Contains(command, "/") {
			want = filepath.Join(c.Dir, command)
		}
		if got := c.HookCommandPath(Hook{Command: []string{command}}); got != want {
			t.Errorf("%s: %s != %s", command, got, want)
		}
	}
	c, err = Parse([]byte("version: 1\nhooks:\n- {id: pre, phase: pre, command: [hook]}\n- {id: post, phase: post, command: [hook], enabled: false}\nlogging: {enabled: false}"), "/config")
	if err != nil {
		t.Fatal(err)
	}
	if c.Hooks[0].OnError != "deny" || c.Hooks[1].OnError != "ignore" || c.Hooks[0].Timeout.D() != DefaultHookTimeout || *c.Hooks[1].Enabled || *c.Logging.Enabled {
		t.Fatalf("hook defaults: %+v", c)
	}
}

func validConfig() *Config {
	c := &Config{Hooks: []Hook{{ID: "a", Phase: PhasePre, Command: []string{"hook"}}, {ID: "b", Phase: PhasePost, Command: []string{"hook"}}}}
	c.ApplyDefaults()
	return c
}

func TestValidationPaths(t *testing.T) {
	cases := []struct {
		path   string
		change func(*Config)
	}{
		{"version", func(c *Config) { c.Version = 2 }},
		{"policy.mode", func(c *Config) { c.Policy.Mode = "bad" }},
		{"policy.unknownMutation", func(c *Config) { c.Policy.UnknownMutation = "bad" }},
		{"policy.protectedUnknown", func(c *Config) { c.Policy.ProtectedUnknown = "bad" }},
		{"policy.protectedRepositories[0]", func(c *Config) { c.Policy.ProtectedRepositories = []string{"org"} }},
		{"policy.rules[0].id", func(c *Config) { c.Policy.Rules = []Rule{{Decision: "deny"}} }},
		{"policy.rules[1].id", func(c *Config) { c.Policy.Rules = []Rule{{ID: "r", Decision: "deny"}, {ID: "r", Decision: "audit"}} }},
		{"policy.rules[0].decision", func(c *Config) { c.Policy.Rules = []Rule{{ID: "r", Decision: "allow"}} }},
		{"hooks[0].id", func(c *Config) { c.Hooks[0].ID = "bad id" }},
		{"hooks[0].id", func(c *Config) { c.Hooks[0].ID = "" }},
		{"hooks[1].id", func(c *Config) { c.Hooks[1].ID = "a" }},
		{"hooks[0].phase", func(c *Config) { c.Hooks[0].Phase = "bad" }},
		{"hooks[0].command", func(c *Config) { c.Hooks[0].Command = nil }},
		{"hooks[0].command", func(c *Config) { c.Hooks[0].Command = []string{" "} }},
		{"hooks[0].onError", func(c *Config) { c.Hooks[0].OnError = "bad" }},
		{"hooks[1].onError", func(c *Config) { c.Hooks[1].OnError = "deny" }},
		{"hooks[0].timeout", func(c *Config) { c.Hooks[0].Timeout = 0 }},
		{"hooks[0].timeout", func(c *Config) { c.Hooks[0].Timeout = Duration(MaxHookTimeout + 1) }},
		{"hooks[0].maxOutputBytes", func(c *Config) { c.Hooks[0].MaxOutputBytes = 0 }},
		{"hooks[0].captureOutput", func(c *Config) { c.Hooks[0].CaptureOutput = true }},
		{"hooks[0].dependsOn[0]", func(c *Config) { c.Hooks[0].DependsOn = []string{"missing"} }},
		{"hooks[0].dependsOn[0]", func(c *Config) { c.Hooks[0].DependsOn = []string{"a"} }},
		{"hooks[0].dependsOn[0]", func(c *Config) { c.Hooks[0].DependsOn = []string{"b"} }},
		{"hooks[1].dependsOn", func(c *Config) {
			c.Hooks[1].Phase = PhasePre
			c.Hooks[0].DependsOn = []string{"b"}
			c.Hooks[1].DependsOn = []string{"a"}
		}},
		{"hooks[0].match.operation[0]", func(c *Config) { c.Hooks[0].Match.Operations = StringList{"["} }},
		{"hooks[0].match.command[0]", func(c *Config) { c.Hooks[0].Match.Commands = StringList{"["} }},
		{"hooks[0].match.hosts[0]", func(c *Config) { c.Hooks[0].Match.Hosts = StringList{"["} }},
		{"hooks[0].match.repositories[0]", func(c *Config) { c.Hooks[0].Match.Repositories = StringList{"a/b/c"} }},
		{"hooks[0].match.repositories[0]", func(c *Config) { c.Hooks[0].Match.Repositories = StringList{"a/["} }},
		{"hooks[0].match.effect[0]", func(c *Config) { c.Hooks[0].Match.Effects = StringList{"bad"} }},
		{"hooks[0].match.level[0]", func(c *Config) { c.Hooks[0].Match.Levels = StringList{"bad"} }},
		{"runtime.hookConcurrency", func(c *Config) { c.Runtime.HookConcurrency = 0 }},
		{"runtime.stdinMaxBytes", func(c *Config) { c.Runtime.StdinMaxBytes = 0 }},
		{"runtime.stdinTimeout", func(c *Config) { c.Runtime.StdinTimeout = 0 }},
		{"runtime.fileMaxBytes", func(c *Config) { c.Runtime.FileMaxBytes = -1 }},
		{"runtime.captureLimit", func(c *Config) { c.Runtime.CaptureLimit = 0 }},
		{"runtime.resolveTimeout", func(c *Config) { c.Runtime.ResolveTimeout = 0 }},
		{"logging.level", func(c *Config) { c.Logging.Level = "debug" }},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			c := validConfig()
			tc.change(c)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.path+":") {
				t.Fatalf("want %s, got %v", tc.path, err)
			}
		})
	}
	c := validConfig()
	c.Version = 2
	c.Hooks[0].ID = ""
	err := c.Validate()
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("not all failures joined: %v", err)
	}
	c = validConfig()
	c.Hooks[0].Timeout = Duration(MaxHookTimeout)
	c.Hooks[1].Phase = PhasePre
	c.Hooks[1].DependsOn = []string{"a"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid DAG/boundary: %v", err)
	}
}

func TestExplicitZeroAndUnknownKeys(t *testing.T) {
	for _, text := range []string{"runtime: {hookConcurrency: 0}", "runtime: {stdinMaxBytes: 0}", "runtime: {stdinTimeout: 0s}", "runtime: {fileMaxBytes: 0}", "runtime: {captureLimit: 0}", "runtime: {resolveTimeout: 0s}", "hooks: [{id: h, phase: pre, command: [h], timeout: 0s}]", "hooks: [{id: h, phase: pre, command: [h], maxOutputBytes: 0}]"} {
		if _, err := Parse([]byte("version: 1\n"+text), ""); err == nil {
			t.Fatalf("explicit zero accepted: %s", text)
		}
	}
	for _, text := range []string{"version: 1\nunknown: true", "version: 1\npolicy: {unknown: true}"} {
		_, err := Parse([]byte(text), "")
		if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("unknown field: %v", err)
		}
	}
	if _, err := Parse([]byte("version: 1\nhooks: [{id: h, phase: pre, command: [h], timeout: nope}]"), ""); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("duration: %v", err)
	}
}

func TestConfigDirectoryPrecedence(t *testing.T) {
	env := map[string]string{"HOME": "/home/test"}
	if Dir(env) != "/home/test/.config/ghok" {
		t.Fatal(Dir(env))
	}
	env["XDG_CONFIG_HOME"] = "/xdg"
	if Dir(env) != "/xdg/ghok" {
		t.Fatal(Dir(env))
	}
	env["GHOK_CONFIG_DIR"] = "/custom"
	if DefaultPath(env) != "/custom/config.yaml" {
		t.Fatal(DefaultPath(env))
	}
}

func writeFixture(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGhAliases(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": filepath.Join(root, "home")}
	locations := []string{filepath.Join(env["HOME"], ".config", "gh"), filepath.Join(root, "xdg", "gh"), filepath.Join(root, "custom")}
	for i, d := range locations {
		if i == 1 {
			env["XDG_CONFIG_HOME"] = filepath.Dir(d)
		}
		if i == 2 {
			env["GH_CONFIG_DIR"] = d
		}
		writeFixture(t, filepath.Join(d, "config.yml"), fmt.Sprintf("git_protocol: ssh\naliases:\n  pm: 'pr merge %d'\n  shell: '!echo test'\n", i))
		got, err := LoadGhAliases(env)
		if err != nil || !reflect.DeepEqual(got, map[string]string{"pm": fmt.Sprintf("pr merge %d", i), "shell": "!echo test"}) {
			t.Fatalf("aliases: %v, %v", got, err)
		}
	}
	env["GH_CONFIG_DIR"] = t.TempDir()
	if got, err := LoadGhAliases(env); err != nil || len(got) != 0 {
		t.Fatalf("missing: %v, %v", got, err)
	}
	writeFixture(t, filepath.Join(env["GH_CONFIG_DIR"], "config.yml"), "aliases: [bad]")
	if _, err := LoadGhAliases(env); err == nil {
		t.Fatal("invalid aliases accepted")
	}
}

func TestProjectTrust(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	repo := filepath.Join(home, "repo")
	cwd := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	project := filepath.Join(repo, ".ghok.yaml")
	global := filepath.Join(root, "global", "config.yaml")
	env := map[string]string{"HOME": home}
	writeFixture(t, global, "version: 1\npolicy: {mode: enforce, unknownMutation: deny}\nhooks: [{id: global, phase: pre, command: [./global-hook]}]\n")
	writeFixture(t, project, "invalid: [")
	c, notes, err := LoadEffective(global, cwd, env)
	if err != nil || len(c.Hooks) != 1 || len(notes) != 1 || notes[0] != "found untrusted project config at "+project+"; ignored" {
		t.Fatalf("untrusted: %+v, %v, %v", c, notes, err)
	}
	base := "version: 1\ntrust:\n  projects: [" + project + "]\npolicy: {mode: enforce, unknownMutation: deny}\nhooks: [{id: global, phase: pre, command: [./global-hook]}]\n"
	writeFixture(t, global, base)
	writeFixture(t, project, "version: 1\npolicy:\n  mode: observe\n  unknownMutation: allow\n  protectedUnknown: audit\n  protectedRepositories: [org/project]\n  rules: [{id: rule, decision: audit}]\nhooks: [{id: project, phase: post, command: [./hooks/hook]}]\n")
	c, notes, err = LoadEffective(global, cwd, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Mode != ModeEnforce || c.Policy.UnknownMutation != UnknownDeny || c.Policy.ProtectedUnknown != UnknownDeny || len(notes) != 3 || !reflect.DeepEqual(c.Policy.ProtectedRepositories, []string{"org/project"}) || len(c.Policy.Rules) != 1 || c.HookCommandPath(c.Hooks[1]) != filepath.Join(repo, "hooks", "hook") || c.HookCommandPath(c.Hooks[0]) != filepath.Join(filepath.Dir(global), "global-hook") {
		t.Fatalf("overlay: %+v, notes %v", c, notes)
	}
	writeFixture(t, global, strings.ReplaceAll(base, "policy: {mode: enforce, unknownMutation: deny}", "policy: {mode: observe, unknownMutation: allow, protectedUnknown: allow}"))
	writeFixture(t, project, "version: 1\npolicy: {mode: enforce, unknownMutation: audit, protectedUnknown: deny}")
	c, _, err = LoadEffective(global, cwd, env)
	if err != nil || c.Policy.Mode != ModeEnforce || c.Policy.UnknownMutation != UnknownAudit || c.Policy.ProtectedUnknown != UnknownDeny {
		t.Fatalf("tightening: %+v, %v", c, err)
	}
	// Omitted project policy must not tighten an explicitly permissive global.
	writeFixture(t, project, "version: 1\nhooks: [{id: project, phase: pre, command: [hook]}]")
	c, _, err = LoadEffective(global, cwd, env)
	if err != nil || c.Policy.UnknownMutation != UnknownAllow || c.Policy.ProtectedUnknown != UnknownAllow {
		t.Fatalf("omitted policy: %+v, %v", c, err)
	}
	for _, section := range []string{"runtime: {}", "logging: {}", "trust: {}", "hooks: [{id: global, phase: pre, command: [hook]}]"} {
		writeFixture(t, project, "version: 1\n"+section)
		if _, _, err := LoadEffective(global, cwd, env); err == nil {
			t.Fatalf("accepted project %s", section)
		}
	}
	// Trust is evaluated against the canonical config path, not a symlink name.
	target := filepath.Join(root, "shared.yaml")
	writeFixture(t, target, "version: 1\nhooks: [{id: linked, phase: pre, command: [./hook]}]")
	if err := os.Remove(project); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, project); err != nil {
		t.Fatal(err)
	}
	c, notes, err = LoadEffective(global, cwd, env)
	if err != nil || len(c.Hooks) != 1 || !strings.Contains(strings.Join(notes, "\n"), "untrusted") {
		t.Fatalf("symlink trust: %v, %v", notes, err)
	}
	writeFixture(t, global, strings.ReplaceAll(base, project, target))
	c, _, err = LoadEffective(global, cwd, env)
	if err != nil || c.HookCommandPath(c.Hooks[1]) != filepath.Join(repo, "hook") {
		t.Fatalf("trusted symlink: %+v, %v", c, err)
	}
	if err := os.Chmod(target, 0666); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, "hook"), "hook")
	if err := os.Chmod(filepath.Join(repo, "hook"), 0777); err != nil {
		t.Fatal(err)
	}
	_, notes, err = LoadEffective(global, cwd, env)
	if err != nil || len(notes) != 2 {
		t.Fatalf("permissions: %v, %v", notes, err)
	}
}

func TestProjectHomeBoundary(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(home, "child")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, ".ghok.yaml"), "version: 1")
	got, err := findProject(cwd, home)
	if err != nil || got != "" {
		t.Fatalf("crossed HOME: %s, %v", got, err)
	}
}

func TestValidateOutcomeMatch(t *testing.T) {
	c, err := Parse([]byte(`version: 1
policy:
  rules:
    - {id: r, decision: deny, match: {result: failure}}
hooks:
  - {id: pre, phase: pre, command: [x], match: {exitCode: [1]}}
  - {id: post, phase: post, command: [x], match: {result: [success, crashed], exitCode: [256]}}
`), t.TempDir()) // Parse validates
	if c != nil {
		t.Fatal("invalid outcome criteria accepted")
	}
	for _, want := range []string{"policy.rules[0].match.result", "hooks[0].match.exitCode", "hooks[1].match.result[1]", "hooks[1].match.exitCode[0]"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s in %v", want, err)
		}
	}
	if _, err := Parse([]byte("version: 1\nhooks:\n  - {id: post, phase: post, command: [x], match: {result: failure, exitCode: [1, 2]}}\n"), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
