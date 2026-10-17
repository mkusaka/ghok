package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalConfig(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, "README"), "x\n")
	trustGit(t, repo, "init", "-q")
	if HasConfig(repo, env) {
		t.Fatal("no config expected")
	}
	path, err := Init(ScopeLocal, repo, env)
	if err != nil || filepath.Base(path) != LocalName {
		t.Fatalf("init: %s %v", path, err)
	}
	exclude, err := ExcludeLocal(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExcludeLocal(repo); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exclude); strings.Count(string(data), "/"+LocalName+"\n") != 1 {
		t.Fatalf("exclude: %q", data)
	}
	if out := trustGit(t, repo, "status", "--porcelain"); strings.Contains(out, LocalName) {
		t.Fatalf("local config not excluded: %s", out)
	}
	// Local only (no .ghok.yaml): its hooks run without trust and come last.
	writeFixture(t, filepath.Join(repo, LocalName), "version: 1\npolicy: {mode: enforce}\nhooks: {pre: [{id: mine, command: [./mine]}]}\n")
	c, notes, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 1 || c.Hooks[0].ID != "mine" || c.Policy.Mode != ModeEnforce || !HasConfig(repo, env) {
		t.Fatalf("local only: %+v %v %v", c, notes, err)
	}
	// With .ghok.yaml: project hooks need trust, local hooks follow them.
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: team, command: [./team]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, true); err != nil {
		t.Fatal(err)
	}
	c, notes, err = LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 2 || c.Hooks[0].ID != "team" || c.Hooks[1].ID != "mine" {
		t.Fatalf("order: %+v %v %v", c.Hooks, notes, err)
	}
	// A committed local config is ignored.
	trustGit(t, repo, "add", "-f", LocalName)
	c, notes, err = LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 1 || !strings.Contains(strings.Join(notes, "\n"), "ignored: tracked by git") {
		t.Fatalf("tracked: %+v %v %v", c.Hooks, notes, err)
	}
	trustGit(t, repo, "rm", "-q", "--cached", LocalName)
	// trust and files are .ghok.yaml-only.
	writeFixture(t, filepath.Join(repo, LocalName), "version: 1\ntrust: {include: [x]}\n")
	if _, notes, _ = LoadEffective("", repo, env); !strings.Contains(strings.Join(notes, "\n"), "local config ignored") {
		t.Fatalf("trust in local: %v", notes)
	}
}
