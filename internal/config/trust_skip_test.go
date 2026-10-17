package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustSkip(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	env := map[string]string{"HOME": home, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(home, "src", "github.com", "me", "repo")
	other := filepath.Join(home, "src", "github.com", "me-evil", "repo")
	outside := filepath.Join(root, "elsewhere", "wt")
	for _, dir := range []string{repo, other} {
		writeFixture(t, filepath.Join(dir, ".ghok.yaml"), "version: 1\npolicy: {mode: enforce}\nhooks: {pre: [{id: gate, command: [./gate]}]}\n")
		trustGit(t, dir, "init", "-q")
		trustGit(t, dir, "add", ".")
		trustGit(t, dir, "commit", "-qm", "init")
	}
	trustGit(t, repo, "worktree", "add", "-q", outside)
	user := func(skip string) {
		t.Helper()
		writeFixture(t, filepath.Join(env["GHOK_CONFIG_DIR"], "config.yaml"), "version: 1\ntrust: {skip: ["+skip+"]}\n")
	}
	active := func(dir string) bool {
		t.Helper()
		c, notes, err := LoadEffective("", dir, env)
		if err != nil {
			t.Fatalf("%s: %v %v", dir, err, notes)
		}
		return len(c.Hooks) == 1 && len(c.IgnoredHooks) == 0
	}
	user(`"~/src/github.com/me/"`)
	if !active(repo) || active(other) {
		t.Fatal("prefix must cover me/ but not me-evil/")
	}
	if !active(outside) {
		t.Fatal("a worktree outside the prefix must match through its main worktree")
	}
	user(`"` + repo + `"`)
	if !active(repo) || active(filepath.Dir(repo)+"/repo2") || active(other) {
		t.Fatal("exact pattern")
	}
	user(`"~/src/github.com/me"`)
	if active(repo) {
		t.Fatal("a pattern without trailing / is exact, not a prefix")
	}
	user(`"relative/path"`)
	if _, _, err := LoadEffective("", repo, env); err == nil || !strings.Contains(err.Error(), "trust.skip") {
		t.Fatalf("relative pattern must be rejected: %v", err)
	}
	writeFixture(t, filepath.Join(env["GHOK_CONFIG_DIR"], "config.yaml"), "version: 1\ntrust: {include: [x]}\n")
	if _, _, err := LoadEffective("", repo, env); err == nil {
		t.Fatal("user trust.include must be rejected")
	}
	// A repository cannot waive its own trust.
	user(`"/nonexistent/"`)
	writeFixture(t, filepath.Join(other, ".ghok.yaml"), "version: 1\ntrust: {skip: [\"/\"]}\nhooks: {pre: [{id: gate, command: [./gate]}]}\n")
	if c, notes, _ := LoadEffective("", other, env); len(c.Hooks) != 0 || !strings.Contains(strings.Join(notes, "\n"), "cannot set trust.skip") {
		t.Fatalf("project trust.skip must drop the project overlay: %+v %v", c.Hooks, notes)
	}
}
