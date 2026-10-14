package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

const fixtureSHA = "0123456789012345678901234567890123456789"
const originConfig = "[remote \"origin\"]\n url = https://github.com/acme/widget.git\n"

func put(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t testing.TB, config string) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dir = real
	put(t, filepath.Join(dir, ".git", "config"), config)
	put(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/feature\n")
	put(t, filepath.Join(dir, ".git", "refs", "heads", "feature"), fixtureSHA+"\n")
	return dir
}

func requireContext(t *testing.T, c model.ResolvedContext, host, repo, branch, sha string) {
	t.Helper()
	if c.Host != host || c.Repository != repo || c.Branch != branch || c.HeadSHA != sha {
		t.Fatalf("context = %+v; want %s %s %s %s", c, host, repo, branch, sha)
	}
	if repo != "" {
		owner, _, _ := strings.Cut(repo, "/")
		if c.Owner != owner {
			t.Fatalf("owner = %q", c.Owner)
		}
	}
}

func TestLocalRemoteURLs(t *testing.T) {
	cases := []struct{ url, host string }{
		{"https://github.com/acme/widget.git", "github.com"},
		{"git@github.com:acme/widget.git", "github.com"},
		{"ssh://git@SSH.GitHub.com:443/acme/widget.git", "github.com"},
		{"https://WWW.GITHUB.COM/acme/widget", "github.com"},
		{"git://github.com/acme/widget.git", "github.com"},
		{"https://code.example.com/acme/widget.git", "code.example.com"},
	}
	for _, tt := range cases {
		t.Run(tt.url, func(t *testing.T) {
			dir := fixture(t, "[remote \"origin\"]\nurl = "+tt.url+"\n")
			r := Resolver{Cwd: dir, Env: map[string]string{}}
			c := r.Local()
			requireContext(t, c, tt.host, "acme/widget", "feature", fixtureSHA)
			if len(c.Unresolved) != 0 || c.Sources["repository"] != "git" {
				t.Fatalf("context = %+v", c)
			}
		})
	}
}

func TestLocalMultipleRemotes(t *testing.T) {
	config := originConfig + "[remote \"upstream\"]\nurl=https://github.com/base/widget.git\n"
	for _, tt := range []struct{ name, extra, repo, reason string }{
		{"ambiguous", "", "base/widget", "multiple remotes; gh may prompt"},
		{"marker", "[remote \"origin\"]\ngh-resolved=base\n", "acme/widget", ""},
		{"named base", "[remote \"origin\"]\ngh-resolved=other/widget\n", "other/widget", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := Resolver{Cwd: fixture(t, config+tt.extra), Env: map[string]string{}}
			c := r.Local()
			requireContext(t, c, "github.com", tt.repo, "feature", fixtureSHA)
			if tt.reason != "" {
				if len(c.Unresolved) != 1 || c.Unresolved[0].Reason != tt.reason {
					t.Fatalf("unresolved = %+v", c.Unresolved)
				}
			} else if len(c.Unresolved) != 0 {
				t.Fatalf("unresolved = %+v", c.Unresolved)
			}
		})
	}
}

func TestLocalRemotePriority(t *testing.T) {
	dir := fixture(t, "[remote \"zzz\"]\nurl=https://github.com/z/r\n[remote \"origin\"]\nurl=https://github.com/o/r\n[remote \"github\"]\nurl=https://github.com/g/r\n")
	c := (&Resolver{Cwd: dir, Env: map[string]string{}}).Local()
	if c.Repository != "g/r" || !hasUnresolved(c.Unresolved, "target.repository") {
		t.Fatalf("context = %+v", c)
	}
}

func TestLocalHeadStates(t *testing.T) {
	for _, state := range []string{"detached", "packed", "unborn", "symbolic"} {
		t.Run(state, func(t *testing.T) {
			dir := fixture(t, originConfig)
			gitdir := filepath.Join(dir, ".git")
			branch, sha := "feature", fixtureSHA
			switch state {
			case "detached":
				put(t, filepath.Join(gitdir, "HEAD"), fixtureSHA)
				branch = ""
			case "packed":
				if err := os.Remove(filepath.Join(gitdir, "refs", "heads", "feature")); err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(gitdir, "packed-refs"), "# pack-refs with: peeled fully-peeled sorted\n"+fixtureSHA+" refs/heads/feature\n^"+fixtureSHA+"\n")
			case "unborn":
				if err := os.Remove(filepath.Join(gitdir, "refs", "heads", "feature")); err != nil {
					t.Fatal(err)
				}
				sha = ""
			case "symbolic":
				put(t, filepath.Join(gitdir, "refs", "heads", "feature"), "ref: refs/heads/other")
				put(t, filepath.Join(gitdir, "refs", "heads", "other"), fixtureSHA)
			}
			c := (&Resolver{Cwd: dir, Env: map[string]string{}}).Local()
			requireContext(t, c, "github.com", "acme/widget", branch, sha)
			if state == "detached" && !hasUnresolved(c.Unresolved, "target.branch") || state == "unborn" && !hasUnresolved(c.Unresolved, "target.sha") {
				t.Fatalf("unresolved = %+v", c.Unresolved)
			}
		})
	}
}

func TestLocalDiscovery(t *testing.T) {
	t.Run("nested", func(t *testing.T) {
		dir := fixture(t, originConfig)
		nested := filepath.Join(dir, "a", "b")
		if err := os.MkdirAll(nested, 0700); err != nil {
			t.Fatal(err)
		}
		c := (&Resolver{Cwd: nested, Env: map[string]string{}}).Local()
		requireContext(t, c, "github.com", "acme/widget", "feature", fixtureSHA)
	})
	t.Run("git dir override", func(t *testing.T) {
		dir := fixture(t, originConfig)
		cwd := filepath.Join(dir, "outside")
		if err := os.Mkdir(cwd, 0700); err != nil {
			t.Fatal(err)
		}
		c := (&Resolver{Cwd: cwd, Env: map[string]string{"GIT_DIR": "../.git"}}).Local()
		requireContext(t, c, "github.com", "acme/widget", "feature", fixtureSHA)
	})
	t.Run("work tree override", func(t *testing.T) {
		dir := fixture(t, originConfig)
		c := (&Resolver{Cwd: t.TempDir(), Env: map[string]string{"GIT_WORK_TREE": dir}}).Local()
		requireContext(t, c, "github.com", "acme/widget", "feature", fixtureSHA)
	})
	t.Run("ceiling", func(t *testing.T) {
		dir := fixture(t, originConfig)
		nested := filepath.Join(dir, "a")
		if err := os.Mkdir(nested, 0700); err != nil {
			t.Fatal(err)
		}
		c := (&Resolver{Cwd: nested, Env: map[string]string{"GIT_CEILING_DIRECTORIES": dir}}).Local()
		if c.GitDir != "" || !hasUnresolved(c.Unresolved, "target.repository") {
			t.Fatalf("context = %+v", c)
		}
	})
	t.Run("no repo", func(t *testing.T) {
		dir := t.TempDir()
		c := (&Resolver{Cwd: dir, Env: map[string]string{"GIT_CEILING_DIRECTORIES": filepath.Dir(dir)}}).Local()
		if c.Repository != "" || !hasUnresolved(c.Unresolved, "target.repository") {
			t.Fatalf("context = %+v", c)
		}
	})
	t.Run("worktree", func(t *testing.T) {
		dir := fixture(t, originConfig)
		work := filepath.Join(dir, "linked")
		admin := filepath.Join(dir, ".git", "worktrees", "linked")
		put(t, filepath.Join(work, ".git"), "gitdir: ../.git/worktrees/linked\n")
		put(t, filepath.Join(admin, "commondir"), "../..\n")
		put(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/work\n")
		put(t, filepath.Join(dir, ".git", "packed-refs"), fixtureSHA+" refs/heads/work\n")
		c := (&Resolver{Cwd: work, Env: map[string]string{}}).Local()
		requireContext(t, c, "github.com", "acme/widget", "work", fixtureSHA)
		if c.GitDir != admin {
			t.Fatalf("gitdir = %s", c.GitDir)
		}
	})
	t.Run("submodule", func(t *testing.T) {
		dir := t.TempDir()
		work := filepath.Join(dir, "module")
		admin := filepath.Join(dir, "metadata")
		put(t, filepath.Join(work, ".git"), "gitdir: ../metadata\n")
		put(t, filepath.Join(admin, "HEAD"), fixtureSHA)
		put(t, filepath.Join(admin, "config"), originConfig)
		c := (&Resolver{Cwd: work, Env: map[string]string{}}).Local()
		requireContext(t, c, "github.com", "acme/widget", "", fixtureSHA)
	})
}

func TestLocalEnvironment(t *testing.T) {
	for _, tt := range []struct{ repo, envHost, host, want string }{
		{"other/repo", "", "github.com", "other/repo"},
		{"other/repo", "code.example.com", "code.example.com", "other/repo"},
		{"HOST.example/other/repo", "ignored.example", "host.example", "other/repo"},
		{"https://github.com/other/repo", "", "github.com", "other/repo"},
	} {
		t.Run(tt.repo+tt.envHost, func(t *testing.T) {
			r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"GH_REPO": tt.repo, "GH_HOST": tt.envHost}}
			c := r.Local()
			requireContext(t, c, tt.host, tt.want, "feature", fixtureSHA)
			if c.Sources["repository"] != "env" {
				t.Fatalf("sources = %+v", c.Sources)
			}
		})
	}
	dir := t.TempDir()
	c := (&Resolver{Cwd: dir, Env: map[string]string{"GH_HOST": "ENTERPRISE.example", "GIT_CEILING_DIRECTORIES": filepath.Dir(dir)}}).Local()
	if c.Host != "enterprise.example" || c.Sources["host"] != "env" {
		t.Fatalf("context = %+v", c)
	}
	c = (&Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"GH_REPO": "invalid"}}).Local()
	if c.Repository != "" || !hasUnresolved(c.Unresolved, "target.repository") {
		t.Fatalf("invalid GH_REPO fell back: %+v", c)
	}
}

func TestLocalConfigSyntax(t *testing.T) {
	dir := fixture(t, "# comment\n[REMOTE \"origin\"] ; comment\n URL = \"https://github.com/acme/\"widget.git # trailing comment\n[include]\npath=extra\n[branch \"feature\"]\nremote=origin\nmerge=refs/heads/main\ngh-merge-base=release\n")
	put(t, filepath.Join(dir, ".git", "extra"), "[remote \"origin\"]\nurl=https://github.com/other/widget.git\n")
	r := Resolver{Cwd: dir, Env: map[string]string{}}
	requireContext(t, r.Local(), "github.com", "other/widget", "feature", fixtureSHA)
	res := model.Result{Operations: []model.Operation{{Family: "pr.create", Target: model.Target{}, Unresolved: []model.Unresolved{{Field: "target.baseBranch"}}}}}
	r.Fill(t.Context(), &res, nil, false)
	if res.Operations[0].Target.BaseBranch != "release" {
		t.Fatalf("target = %+v", res.Operations[0].Target)
	}
}

func TestLocalMalformedMetadata(t *testing.T) {
	for _, config := range []string{"[remote \"origin\"", "[remote \"origin\"]\nurl=\"unterminated", "[remote \"origin\"]\nurl=bad\\q", "url=x", "[includeif \"gitdir:x\"]\npath=x"} {
		t.Run(config, func(t *testing.T) {
			c := (&Resolver{Cwd: fixture(t, config), Env: map[string]string{}}).Local()
			if c.Repository != "" || !hasUnresolved(c.Unresolved, "target.repository") {
				t.Fatalf("context = %+v", c)
			}
		})
	}
	dir := fixture(t, "[include]\npath=config\n")
	if c := (&Resolver{Cwd: dir, Env: map[string]string{}}).Local(); !hasUnresolved(c.Unresolved, "target.repository") {
		t.Fatalf("include cycle = %+v", c)
	}
	dir = t.TempDir()
	put(t, filepath.Join(dir, ".git"), "nonsense")
	if c := (&Resolver{Cwd: dir, Env: map[string]string{}}).Local(); !hasUnresolved(c.Unresolved, "target.repository") {
		t.Fatalf("gitfile = %+v", c)
	}
}

func TestLocalSSHTranslation(t *testing.T) {
	for _, tt := range []struct {
		config, host string
		unresolved   bool
	}{
		{"Host work\n HostName github.com\n", "github.com", false},
		{"Host w* !wrong\n HostName CODE.example.com\nHost *\n HostName ignored.example\n", "code.example.com", false},
		{"Include config.d/*\n", "", true},
		{"Match exec something\n HostName github.com\n", "", true},
	} {
		t.Run(tt.config, func(t *testing.T) {
			dir := fixture(t, "[remote \"origin\"]\nurl=git@work:acme/widget.git\n")
			home := t.TempDir()
			put(t, filepath.Join(home, ".ssh", "config"), tt.config)
			c := (&Resolver{Cwd: dir, Env: map[string]string{"HOME": home, "GH_HOST": "fallback.example"}}).Local()
			if c.Repository != "acme/widget" || c.Host != tt.host || hasUnresolved(c.Unresolved, "target.host") != tt.unresolved {
				t.Fatalf("context = %+v", c)
			}
		})
	}
}

func TestLocalCachedCopies(t *testing.T) {
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{}}
	first := r.Local()
	first.Sources["repository"] = "wrong"
	if c := r.Local(); c.Sources["repository"] != "git" {
		t.Fatalf("caller mutated cache: %+v", c)
	}
}

func BenchmarkLocal(b *testing.B) {
	dir := fixture(b, originConfig)
	b.ReportAllocs()
	for b.Loop() {
		r := Resolver{Cwd: dir, Env: map[string]string{}}
		_ = r.Local()
	}
}
