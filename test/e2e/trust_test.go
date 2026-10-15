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

func writeTrustProject(t *testing.T, s *sandbox, command ...string) (string, []byte) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	h := s.hook("project", "pre")
	h.OnError = config.OnErrorAllow
	if len(command) != 0 {
		h.Command = command
	}
	defaults := config.Config{Hooks: []config.Hook{h}}
	defaults.ApplyDefaults()
	h = defaults.Hooks[0]
	body, err := yaml.Marshal(struct {
		Version int             `yaml:"version"`
		Policy  config.Policy   `yaml:"policy"`
		Hooks   config.HookList `yaml:"hooks"`
	}{Version: 1, Policy: config.Policy{Mode: config.ModeEnforce, UnknownMutation: "audit", ProtectedUnknown: "deny", Rules: []config.Rule{{ID: "project-deny", Decision: "deny", Match: config.Match{Commands: []string{"pr merge"}}}}}, Hooks: config.HookList{h}})
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
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 77)
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
	if err := os.WriteFile(project, bytes.ReplaceAll(body, []byte("project.json"), []byte("project-updated.json")), 0600); err != nil {
		t.Fatal(err)
	}
	assertIgnoredProjectHook(t, s, "changed since trust")
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 77)
	if len(s.records()) != 3 {
		t.Fatal("edited project policy did not prevent gh")
	}
	s.management(nil, "config", "trust", project).code(t, 0)
	s.management(nil, "config", "untrust", project).code(t, 0)
	s.management(nil, "config", "untrust", project).code(t, 0)
	assertIgnoredProjectHook(t, s, "untrusted")
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	if err := os.WriteFile(project, body, 0600); err != nil {
		t.Fatal(err)
	}
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
}

func TestProjectTrustRelativeExecutableAcrossWorktrees(t *testing.T) {
	s := newSandbox(t)
	project, body := writeTrustProject(t, s, "./hooks/project")
	if err := os.Remove(filepath.Join(s.cwd, ".git")); err != nil {
		t.Fatal(err)
	}
	executable, err := os.ReadFile(fakeBinary)
	if err != nil {
		t.Fatal(err)
	}
	sharedGit := filepath.Join(s.root, "shared.git")
	if err := os.Mkdir(sharedGit, 0700); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(s.root, "other-worktree")
	if err := os.Mkdir(second, 0700); err != nil {
		t.Fatal(err)
	}
	// Identical content in another checkout still requires explicit approval.
	for _, worktree := range []string{s.cwd, second} {
		if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+sharedGit+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(worktree, "hooks"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktree, "hooks", "project"), executable, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktree, ".ghok.yaml"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := s.management(nil, "config", "trust", project)
	result.code(t, 0)
	for _, want := range []string{"hooks/project", "project command:"} {
		if !bytes.Contains(result.stdout, []byte(want)) {
			t.Fatalf("trust output omitted %q: %s", want, result.stdout)
		}
	}
	assertNoProjectHook(t, s)
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	if err := os.Remove(filepath.Join(s.root, "project.json")); err != nil {
		t.Fatal(err)
	}
	s.cwd = second
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	assertIgnoredProjectHook(t, s, "untrusted")
	s.management(nil, "config", "trust").code(t, 0)
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	if err := os.Remove(filepath.Join(s.root, "project.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "hooks", "project"), append(executable, []byte("\nchanged executable\n")...), 0700); err != nil {
		t.Fatal(err)
	}
	assertIgnoredProjectHook(t, s, "changed since trust")
	result = s.gh(nil, "version")
	result.code(t, 0)
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 77)
	assertNoProjectHook(t, s)
	if len(s.records()) != 4 {
		t.Fatal("changed executable bypassed project deny rule")
	}
}

func TestProjectTrustCorruption(t *testing.T) {
	s := newSandbox(t)
	writeTrustProject(t, s)
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	s.management(nil, "config", "trust").code(t, 0)
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.cfg), "trust.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	result := s.gh(nil, "version")
	result.code(t, 0)
	assertNoProjectHook(t, s)
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 77)
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
	var corrupt bool
	for _, check := range checks {
		corrupt = corrupt || bytes.Contains([]byte(check.Detail), []byte("trust.json"))
	}
	if !corrupt {
		t.Fatalf("doctor omitted trust diagnostics: %+v", checks)
	}
}

func TestProjectTrustPathsAndUsage(t *testing.T) {
	s := newSandbox(t)
	for _, command := range []string{"trust", "untrust"} {
		s.management(nil, "config", command).code(t, 1)
		s.management(nil, "config", command, "missing.yaml").code(t, 1)
		s.management(nil, "config", command, "one", "two").code(t, 2)
		flag := "--list"
		if command == "untrust" {
			flag = "--all"
		}
		s.management(nil, "config", command, flag, "one").code(t, 2)
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
	body := "version: 1\nhooks: {pre: [{id: project, command: " + string(command) + "}]}\n"
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
	target := filepath.Join(s.cwd, "project-config.yaml")
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

func TestProjectTrustListAndClearAll(t *testing.T) {
	s := newSandbox(t)
	project, body := writeTrustProject(t, s)
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust").code(t, 0)
	first := s.management(nil, "config", "trust", "--list")
	first.code(t, 0)
	for _, want := range []string{"firstPath: " + project, "hook IDs: project", "trustedAt: "} {
		if !bytes.Contains(first.stdout, []byte(want)) {
			t.Fatalf("trust list omitted %q: %s", want, first.stdout)
		}
	}
	if err := os.WriteFile(project, append(body, []byte("\n# second digest\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust").code(t, 0)
	second := s.management(nil, "config", "trust", "--list")
	second.code(t, 0)
	if bytes.Count(second.stdout, []byte("trustedAt")) != 1 {
		t.Fatalf("trust must keep one record per checkout: %s", second.stdout)
	}
	s.management(nil, "config", "untrust", "--all").code(t, 0)
	empty := s.management(nil, "config", "trust", "--list")
	empty.code(t, 0)
	if bytes.Contains(empty.stdout, []byte("trustedAt")) {
		t.Fatalf("untrust --all retained entries: %s", empty.stdout)
	}
	for _, version := range [][]byte{body, append(append([]byte(nil), body...), []byte("\n# second digest\n")...)} {
		if err := os.WriteFile(project, version, 0600); err != nil {
			t.Fatal(err)
		}
		s.gh(nil, "version").code(t, 0)
		assertNoProjectHook(t, s)
	}
}

func TestProjectTrustIncludeEditRetainsRules(t *testing.T) {
	s := newSandbox(t)
	project, body := writeTrustProject(t, s)
	body = append(body, []byte("\ntrust:\n  include: [policy-data.txt, optional.txt]\n")...)
	if err := os.WriteFile(project, body, 0600); err != nil {
		t.Fatal(err)
	}
	included := filepath.Join(s.cwd, "policy-data.txt")
	if err := os.WriteFile(included, []byte("reviewed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust").code(t, 0)
	list := s.management(nil, "config", "trust", "--list")
	list.code(t, 0)
	for _, want := range []string{"policy-data.txt", "optional.txt", "missing"} {
		if !bytes.Contains(list.stdout, []byte(want)) {
			t.Fatalf("trust list omitted included file %q: %s", want, list.stdout)
		}
	}
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	if err := os.Remove(filepath.Join(s.root, "project.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(included, []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	assertIgnoredProjectHook(t, s, "changed since trust")
	s.gh(nil, "version").code(t, 0)
	assertNoProjectHook(t, s)
	before := len(s.records())
	s.gh(nil, "pr", "merge", "12", "-R", "acme/project").code(t, 77)
	if len(s.records()) != before {
		t.Fatal("included-file edit bypassed project deny rule")
	}
	assertNoProjectHook(t, s)
}
