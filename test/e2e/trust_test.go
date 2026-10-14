//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"go.yaml.in/yaml/v3"
)

func writeTrustProject(t *testing.T, s *sandbox) (string, []byte) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	h := s.hook("project", "pre")
	defaults := config.Config{Hooks: []config.Hook{h}}
	defaults.ApplyDefaults()
	h = defaults.Hooks[0]
	body, err := yaml.Marshal(struct {
		Version int           `yaml:"version"`
		Policy  config.Policy `yaml:"policy"`
		Hooks   []config.Hook `yaml:"hooks"`
	}{Version: 1, Policy: config.Policy{Mode: config.ModeEnforce, UnknownMutation: "audit", ProtectedUnknown: "deny", Rules: []config.Rule{{ID: "project-deny", Decision: "deny", Match: config.Match{Commands: []string{"pr merge"}}}}}, Hooks: []config.Hook{h}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.cwd, ".ghok.yaml")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path, body
}

func assertNoProjectHook(t *testing.T, s *sandbox) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(s.root, "project.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project hook unexpectedly ran: %v", err)
	}
}

func assertIgnoredProjectHook(t *testing.T, s *sandbox, reason string) {
	t.Helper()
	result := s.management(nil, "hooks", "list", "--json")
	result.code(t, 0)
	var rows []struct {
		ID      string `json:"id"`
		Ignored string `json:"ignored"`
	}
	decodeJSON(t, result.stdout, &rows)
	if len(rows) != 1 || rows[0].ID != "project" || rows[0].Ignored != reason {
		t.Fatalf("ignored hooks=%+v", rows)
	}
	for _, args := range [][]string{{"hooks", "list"}, {"explain", "--", "version"}, {"config", "validate"}} {
		result = s.management(nil, args...)
		result.code(t, 0)
		if !bytes.Contains(result.stdout, []byte("ignored ("+reason+")")) || !bytes.Contains(result.stdout, []byte("ghok config trust")) {
			t.Fatalf("missing ignored-hook explanation: %s", result.stdout)
		}
	}
}

func TestProjectHookTrustTransitions(t *testing.T) {
	s := newSandbox(t)
	project, body := writeTrustProject(t, s)
	assertIgnoredProjectHook(t, s, "untrusted")
	result := s.gh(nil, "version")
	result.code(t, 0)
	if len(result.stderr) != 0 {
		t.Fatalf("untrusted wrapper must remain silent: %s", result.stderr)
	}
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 1)
	if len(s.records()) != 1 {
		t.Fatal("untrusted project policy did not prevent gh")
	}
	result = s.management(nil, "config", "trust")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("project")) || !bytes.Contains(result.stdout, []byte(fakeBinary)) {
		t.Fatalf("trust omitted activated command: %s", result.stdout)
	}
	assertNoProjectHook(t, s)
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	if err := os.Remove(filepath.Join(s.root, "project.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, append(body, []byte("\n# edited\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	assertIgnoredProjectHook(t, s, "changed since trust")
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 1)
	if len(s.records()) != 3 {
		t.Fatal("edited project policy did not prevent gh")
	}
	s.management(nil, "config", "trust", project).code(t, 0)
	s.management(nil, "config", "untrust", project).code(t, 0)
	s.management(nil, "config", "untrust", project).code(t, 0)
	assertIgnoredProjectHook(t, s, "untrusted")
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
}

func TestProjectTrustCorruptionAndDeprecatedKey(t *testing.T) {
	s := newSandbox(t)
	project, _ := writeTrustProject(t, s)
	s.configure(func(c *config.Config) { c.Trust.Projects = []string{project} })
	result := s.management(nil, "config", "validate")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("deprecated")) {
		t.Fatalf("missing deprecated trust key note: %s", result.stdout)
	}
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	s.management(nil, "config", "trust").code(t, 0)
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.cfg), "trust.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	result = s.gh(nil, "version")
	result.code(t, 0)
	if len(result.stderr) != 0 {
		t.Fatalf("corrupt-store wrapper must remain silent: %s", result.stderr)
	}
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 1)
	if len(s.records()) != 2 {
		t.Fatal("corrupt trust store bypassed project policy")
	}
	result = s.management(nil, "config", "validate")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("trust.json")) {
		t.Fatalf("missing corrupt-store diagnostic: %s", result.stdout)
	}
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
	result = s.management(nil, "doctor", "--json")
	result.code(t, 0)
	var checks []struct {
		Detail string `json:"detail"`
	}
	decodeJSON(t, result.stdout, &checks)
	var corrupt, deprecated bool
	for _, check := range checks {
		corrupt = corrupt || bytes.Contains([]byte(check.Detail), []byte("trust.json"))
		deprecated = deprecated || bytes.Contains([]byte(check.Detail), []byte("trust.projects"))
	}
	if !corrupt || !deprecated {
		t.Fatalf("doctor omitted trust diagnostics: %+v", checks)
	}
}

func TestProjectTrustPathsAndUsage(t *testing.T) {
	s := newSandbox(t)
	for _, command := range []string{"trust", "untrust"} {
		s.management(nil, "config", command).code(t, 1)
		s.management(nil, "config", command, "missing.yaml").code(t, 1)
		s.management(nil, "config", command, "one", "two").code(t, 2)
		outside := filepath.Join(s.root, ".ghok.yaml")
		if err := os.WriteFile(outside, []byte("version: 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		s.management(nil, "config", command, outside).code(t, 1)
	}
	project, _ := writeTrustProject(t, s)
	alias := filepath.Join(s.root, "repo-link")
	if err := os.Symlink(s.cwd, alias); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust", filepath.Join(alias, ".ghok.yaml")).code(t, 0)
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	s.management(nil, "config", "untrust", project).code(t, 0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg), "trust.json"))
	if err != nil || !json.Valid(data) {
		t.Fatalf("invalid persisted trust store: %v", err)
	}
}

func TestProjectTrustCommandRedaction(t *testing.T) {
	s := newSandbox(t)
	project, _ := writeTrustProject(t, s)
	secret := "trust-command-" + "private-value"
	s.env["GH_TOKEN"] = secret
	command, err := json.Marshal([]string{fakeBinary, "--token", secret})
	if err != nil {
		t.Fatal(err)
	}
	body := "version: 1\nhooks: [{id: project, phase: pre, command: " + string(command) + "}]\n"
	if err := os.WriteFile(project, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"hooks", "list", "--json"}, {"config", "trust"}, {"hooks", "list", "--json"}} {
		result := s.management(nil, args...)
		result.code(t, 0)
		if bytes.Contains(result.stdout, []byte(secret)) || bytes.Contains(result.stderr, []byte(secret)) || !bytes.Contains(result.stdout, []byte("[REDACTED]")) {
			t.Fatalf("project command redaction: stdout=%s stderr=%s", result.stdout, result.stderr)
		}
	}
	assertNoProjectHook(t, s)
	s.absentRecord()
}

func TestProjectTrustSymlinkFile(t *testing.T) {
	s := newSandbox(t)
	project, _ := writeTrustProject(t, s)
	target := filepath.Join(s.root, "project-config.yaml")
	if err := os.Rename(project, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, project); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust", project).code(t, 0)
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	if err := os.Remove(filepath.Join(s.root, "project.json")); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "untrust", project).code(t, 0)
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
}
