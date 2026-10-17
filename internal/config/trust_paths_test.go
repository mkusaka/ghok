package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic metadata avoids creating worktrees outside the wt workflow.
func pathTrustFixture(t *testing.T) (repo, linked, home string, env map[string]string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "home")
	repo = filepath.Join(home, "src", "me", "repo")
	linked = filepath.Join(root, "elsewhere", "wt")
	env = map[string]string{"HOME": home, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	for _, dir := range []string{repo, linked} {
		writeFixture(t, filepath.Join(dir, ".ghok.yaml"), "version: 1\npolicy: {mode: enforce}\nhooks: {pre: [{id: gate, command: [./gate]}]}\ntransforms: {pre: [{id: edit, command: [./filter]}]}\n")
	}
	gitdir := filepath.Join(repo, ".git", "worktrees", "wt")
	writeFixture(t, filepath.Join(gitdir, "commondir"), "../..\n")
	writeFixture(t, filepath.Join(linked, ".git"), "gitdir: "+gitdir+"\n")
	return repo, linked, home, env
}

func TestTrustPathsMatch(t *testing.T) {
	repo, linked, home, env := pathTrustFixture(t)
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(filepath.Dir(repo), alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		trust Trust
		root  string
		key   string
		entry string
	}{
		{"exact", Trust{Repositories: []string{repo}}, repo, "repositories", repo},
		{"exact trailing slash", Trust{Repositories: []string{repo + "/"}}, repo, "repositories", repo + "/"},
		{"exact excludes descendants", Trust{Repositories: []string{filepath.Dir(repo)}}, repo, "", ""},
		{"exact excludes sibling", Trust{Repositories: []string{repo}}, repo + "2", "", ""},
		{"linked exact", Trust{Repositories: []string{repo}}, linked, "repositories", repo},
		{"linked checkout exact", Trust{Repositories: []string{linked}}, linked, "repositories", linked},
		{"directory", Trust{Directories: []string{"~/src/me"}}, repo, "directories", "~/src/me"},
		{"directory trailing slash", Trust{Directories: []string{"~/src/me/"}}, repo, "directories", "~/src/me/"},
		{"directory itself", Trust{Directories: []string{repo}}, repo, "directories", repo},
		{"directory boundary", Trust{Directories: []string{filepath.Dir(repo)}}, filepath.Join(home, "src", "me-evil", "repo"), "", ""},
		{"linked outside directory", Trust{Directories: []string{"~/src/me"}}, linked, "directories", "~/src/me"},
		{"linked checkout directory", Trust{Directories: []string{filepath.Dir(linked)}}, linked, "directories", filepath.Dir(linked)},
		{"symlink directory", Trust{Directories: []string{alias}}, repo, "directories", alias},
		{"filesystem root", Trust{Directories: []string{string(filepath.Separator)}}, repo, "directories", string(filepath.Separator)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, entry := TrustMatch(tc.trust, tc.root, env)
			if key != tc.key || entry != tc.entry {
				t.Fatalf("got %q %q; want %q %q", key, entry, tc.key, tc.entry)
			}
		})
	}
	sub := filepath.Join(linked, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if key, _ := TrustMatch(Trust{Directories: []string{filepath.Dir(linked)}}, sub, env); key != "directories" {
		t.Fatal("subdirectory matching must retain the linked checkout candidate")
	}
}

func TestTrustPathsLoadEffective(t *testing.T) {
	repo, linked, _, env := pathTrustFixture(t)
	user := DefaultPath(env)
	writeFixture(t, user, "version: 1\ntrust: {directories: [\"~/src/me\"]}\n")
	for _, dir := range []string{repo, linked} {
		c, notes, err := LoadEffective("", dir, env)
		if err != nil || len(c.Hooks) != 1 || len(c.Transforms) != 1 || len(c.IgnoredHooks) != 0 {
			t.Fatalf("trusted paths must activate project hooks and transforms: %+v, %v, %v", c, notes, err)
		}
		if !strings.Contains(strings.Join(notes, "\n"), "trust.directories") {
			t.Fatalf("missing matched trust entry: %v", notes)
		}
	}
}

func TestTrustPathsUserValidation(t *testing.T) {
	for _, key := range []string{"repositories", "directories"} {
		for _, path := range []string{"owner/repo", "./relative", "~", "~other/repo", ""} {
			t.Run(key+"/"+path, func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "config.yaml")
				writeFixture(t, file, "version: 1\ntrust: {"+key+": [\""+path+"\"]}\n")
				_, err := Load(file)
				if err == nil || !strings.Contains(err.Error(), "trust."+key) || !strings.Contains(err.Error(), "filesystem paths (absolute or ~/...), not owner/repo") {
					t.Fatalf("likely owner/repo mistake must be explained: %v", err)
				}
			})
		}
	}
	for _, body := range []string{
		"trust: {include: []}",
		"trust: &trust {include: []}",
		"<<: {trust: {include: []}}",
		"trust: {skip: [/]}",
	} {
		file := filepath.Join(t.TempDir(), "config.yaml")
		writeFixture(t, file, "version: 1\n"+body+"\n")
		if _, err := Load(file); err == nil {
			t.Fatalf("user config must reject %s", body)
		}
	}
}

func TestTrustPathsProjectOverlayRejected(t *testing.T) {
	for _, key := range []string{"repositories", "directories"} {
		for _, body := range []string{
			"trust: {KEY: [/]}",
			"<<: {trust: {KEY: []}}",
			"trust: {<<: {KEY: []}}",
			"trust: &t {KEY: []}\n<<: {trust: *t}",
			"policy: &t {KEY: []}\ntrust: *t",
		} {
			t.Run(key+"/"+body, func(t *testing.T) {
				repo, _, _, env := pathTrustFixture(t)
				writeFixture(t, DefaultPath(env), "version: 1\n")
				body = strings.ReplaceAll(body, "KEY", key)
				writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\n"+body+"\nhooks: {pre: [{id: gate, command: [./gate]}]}\n")
				c, notes, err := LoadEffective("", repo, env)
				if err != nil || len(c.Hooks) != 0 || !strings.Contains(strings.Join(notes, "\n"), "cannot set trust."+key) {
					t.Fatalf("project cannot waive its own trust: %+v %v %v", c, notes, err)
				}
			})
		}
	}
}

func TestTrustPathsLocalForbidden(t *testing.T) {
	for _, body := range []string{
		"trust: {repositories: []}",
		"trust: {directories: []}",
		"trust: {include: []}",
		"<<: {trust: {include: []}}",
	} {
		t.Run(body, func(t *testing.T) {
			repo, _, _, env := pathTrustFixture(t)
			writeFixture(t, DefaultPath(env), "version: 1\n")
			writeFixture(t, filepath.Join(repo, LocalName), "version: 1\n"+body+"\nhooks: {pre: [{id: local, command: [./local]}]}\n")
			c, notes, err := LoadEffective("", repo, env)
			if err != nil || len(c.Hooks) != 0 || !strings.Contains(strings.Join(notes, "\n"), "local config ignored") {
				t.Fatalf("local trust forbidden: %+v %v %v", c, notes, err)
			}
		})
	}
}
