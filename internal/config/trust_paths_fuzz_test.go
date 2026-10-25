package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzTrustPathMatch compares TrustMatch with an oracle built from the
// fixture's known layout: a checkout (or its main worktree, for the linked
// one) is trusted by an exact repositories entry, or by a directories entry
// that is the path itself or a "/"-bounded prefix of it, after symlinks.
func FuzzTrustPathMatch(f *testing.F) {
	for _, s := range []struct {
		directories bool
		base        uint8
		path        string
		root        uint8
	}{
		{true, 0, "src/me", 0}, {true, 0, "src/me", 1}, {true, 0, "src/me", 2}, {true, 0, "src/me/", 0},
		{true, 0, "src/me", 3}, {true, 0, "alias", 0}, {true, 0, "alias", 5}, {true, 0, "src", 4},
		{false, 0, "src/me/repo", 3}, {false, 0, "src/me", 0}, {false, 1, "src/me/repo/", 5},
		{true, 0, "src/me/../me-evil", 1}, {true, 3, "", 0}, {true, 2, "elsewhere", 3},
		{false, 2, "elsewhere/wt", 3}, {true, 0, "alias/nope", 0}, {true, 0, "src/me.", 2},
	} {
		f.Add(s.directories, s.base, s.path, s.root)
	}
	f.Fuzz(func(t *testing.T, directories bool, base uint8, path string, rootIndex uint8) {
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		home := filepath.Join(dir, "home")
		main := filepath.Join(home, "src", "me", "repo")
		linked := filepath.Join(dir, "elsewhere", "wt")
		for _, repo := range []string{main, filepath.Join(home, "src", "me-evil", "repo"), filepath.Join(home, "src", "me.x", "repo")} {
			if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		gitdir := filepath.Join(main, ".git", "worktrees", "wt")
		writeFixture(t, filepath.Join(gitdir, "commondir"), "../..\n")
		writeFixture(t, filepath.Join(linked, ".git"), "gitdir: "+gitdir+"\n")
		if err := os.Symlink(filepath.Join(home, "src", "me"), filepath.Join(home, "alias")); err != nil {
			t.Fatal(err)
		}
		for _, sub := range []string{filepath.Join(main, "sub"), filepath.Join(linked, "sub")} {
			if err := os.Mkdir(sub, 0o700); err != nil {
				t.Fatal(err)
			}
		}

		// root as handed to TrustMatch, and the checkouts trust may name for it.
		roots := []struct {
			root       string
			candidates []string
		}{
			{main, []string{main}},
			{filepath.Join(home, "src", "me-evil", "repo"), []string{filepath.Join(home, "src", "me-evil", "repo")}},
			{filepath.Join(home, "src", "me.x", "repo"), []string{filepath.Join(home, "src", "me.x", "repo")}},
			{linked, []string{linked, main}},
			{filepath.Join(linked, "sub"), []string{linked, main}},
			{filepath.Join(home, "alias", "repo", "sub"), []string{main}},
		}
		r := roots[int(rootIndex)%len(roots)]
		entry := []string{"~/", home + "/", dir + "/", "/"}[int(base)%4] + path
		env := map[string]string{"HOME": home}

		expanded := entry
		if rest, ok := strings.CutPrefix(entry, "~/"); ok {
			expanded = home + "/" + rest
		}
		resolved := filepath.Clean(expanded)
		if real, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = real
		}
		want := false
		for _, c := range r.candidates {
			want = want || c == resolved || directories && (resolved == "/" || strings.HasPrefix(c, resolved+"/"))
		}

		trust, kind := Trust{Repositories: []string{entry}}, "repositories"
		if directories {
			trust, kind = Trust{Directories: []string{entry}}, "directories"
		}
		key, got := TrustMatch(trust, r.root, env)
		if (key != "") != want || key != "" && (key != kind || got != entry) {
			t.Fatalf("%s %q for root %s: got %q %q, want match=%t (resolved %s, candidates %v)", kind, entry, r.root, key, got, want, resolved, r.candidates)
		}
	})
}
