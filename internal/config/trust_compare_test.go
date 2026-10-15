package config

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func trustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), git, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_AUTHOR_NAME=Trust Test", "GIT_AUTHOR_EMAIL=trust@example.invalid", "GIT_COMMITTER_NAME=Trust Test", "GIT_COMMITTER_EMAIL=trust@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func trustRepositoryFixture(t *testing.T) (string, map[string]string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\npolicy: {mode: enforce}\nhooks: [{id: review, phase: pre, command: [./hook]}]\n")
	writeFixture(t, filepath.Join(repo, "hook"), "reviewed code\n")
	trustGit(t, repo, "init", "-b", "main")
	trustGit(t, repo, "add", ".")
	trustGit(t, repo, "commit", "-m", "reviewed")
	commit := trustGit(t, repo, "rev-parse", "HEAD")
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	return repo, env, commit
}

func TestTrustProvenanceAndRefresh(t *testing.T) {
	repo, env, commit := trustRepositoryFixture(t)
	trustGit(t, repo, "remote", "add", "origin", "git@GitHub.com:Org/Repo.git")
	trustGit(t, repo, "pack-refs", "--all")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	store, _ := readTrustStore(env)
	var digest string
	var before trustEntry
	for digest, before = range store {
		if before.Commit != commit || before.CommonDir != filepath.Join(repo, ".git") || before.RemoteURL != "github.com/Org/Repo" {
			t.Fatalf("provenance: %+v", before)
		}
	}
	writeFixture(t, filepath.Join(repo, "uncovered"), "not part of trust")
	trustGit(t, repo, "add", ".")
	trustGit(t, repo, "commit", "-m", "uncovered change")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	after, _ := readTrustStore(env)
	if len(after) != 1 || after[digest].Commit != trustGit(t, repo, "rev-parse", "HEAD") || after[digest].FirstPath != before.FirstPath || after[digest].TrustedAt.Before(before.TrustedAt) {
		t.Fatalf("same content must retain its grant and refresh provenance: %+v", after)
	}
}

func TestTrustComparisonWorktreeCloneAndShallow(t *testing.T) {
	for _, kind := range []string{"worktree", "clone", "shallow"} {
		t.Run(kind, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			remote := "https://github.com/org/repo.git"
			if kind == "shallow" {
				remote = "file://" + repo
			}
			trustGit(t, repo, "remote", "add", "origin", remote)
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(repo, "hook"), "new code to review\n")
			trustGit(t, repo, "add", ".")
			trustGit(t, repo, "commit", "-m", "hook changed")
			other := filepath.Join(filepath.Dir(repo), "other")
			switch kind {
			case "worktree":
				// A linked-worktree layout: its own HEAD, shared refs/config/objects.
				gitdir := filepath.Join(repo, ".git", "worktrees", "other")
				writeFixture(t, filepath.Join(gitdir, "commondir"), "../..\n")
				writeFixture(t, filepath.Join(gitdir, "HEAD"), trustGit(t, repo, "rev-parse", "HEAD")+"\n")
				writeFixture(t, filepath.Join(other, ".git"), "gitdir: "+gitdir+"\n")
				for _, path := range []string{".ghok.yaml", "hook"} {
					data, err := os.ReadFile(filepath.Join(repo, path))
					if err != nil {
						t.Fatal(err)
					}
					writeFixture(t, filepath.Join(other, path), string(data))
				}
			case "clone":
				trustGit(t, repo, "clone", "--no-local", repo, other)
				trustGit(t, other, "remote", "set-url", "origin", "ssh://git@GITHUB.com/org/repo/")
			case "shallow":
				trustGit(t, repo, "clone", "--depth=1", "file://"+repo, other)
			}
			c, notes, err := LoadEffective("", other, env)
			if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].Reason != "changed since trust" || c.Policy.Mode != ModeEnforce || !reflect.DeepEqual(c.IgnoredProject.ChangedFiles, []string{"hook"}) {
				t.Fatalf("comparison must not authorize changed hooks or discard policy: %+v, %v, %v", c, notes, err)
			}
			text := strings.Join(notes, "\n")
			for _, want := range []string{"Previous trust: trustedAt:", filepath.Join(repo, ".ghok.yaml"), "Changed files since trust: hook"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			_, _, _, diff, err := TrustProject("", other, env, true, false)
			if err != nil || !strings.Contains(string(diff), "-reviewed code\n+new code to review\n") {
				t.Fatalf("stored baseline must work independently of Git objects: %s, %v", diff, err)
			}
		})
	}
}

func TestPreviousTrustSelection(t *testing.T) {
	file := func(path string) trustedFile { return trustedFile{Path: path, SHA256: contentHash([]byte(path))} }
	current := trustEntry{FirstPath: "/repo/.ghok.yaml", ConfigPath: ".ghok.yaml", CommonDir: "/repo/.git", RemoteURL: "github.com/org/repo", Files: []trustedFile{file(".ghok.yaml"), file("hook"), file("data"), file(".gitleaks.toml")}}
	now := time.Now()
	fallback := trustEntry{FirstPath: "/fallback/.ghok.yaml", ConfigPath: ".ghok.yaml", Files: []trustedFile{file("hook"), file("data")}, TrustedAt: now}
	remote := trustEntry{FirstPath: "/clone/.ghok.yaml", RemoteURL: current.RemoteURL, TrustedAt: now.Add(-time.Hour)}
	path := trustEntry{FirstPath: current.FirstPath, TrustedAt: now.Add(-2 * time.Hour)}
	common := trustEntry{FirstPath: "/worktree/.ghok.yaml", CommonDir: current.CommonDir, TrustedAt: now.Add(-3 * time.Hour)}
	newerCommon := common
	newerCommon.FirstPath, newerCommon.TrustedAt = "/newer-worktree/.ghok.yaml", common.TrustedAt.Add(time.Minute)
	store := map[string]trustEntry{"fallback": fallback, "remote": remote, "path": path, "common": common, "newer-common": newerCommon}
	for _, want := range []trustEntry{newerCommon, common, path, remote, fallback} {
		got := previousTrustEntry(current, store)
		if got == nil || !reflect.DeepEqual(*got, want) {
			t.Fatalf("wanted baseline %s, got %+v", want.FirstPath, got)
		}
		for key, entry := range store {
			if entry.FirstPath == want.FirstPath {
				delete(store, key)
			}
		}
	}
	unrelated := trustEntry{FirstPath: "/unrelated/.ghok.yaml", ConfigPath: ".ghok.yaml", Files: []trustedFile{file(".gitleaks.toml"), {Path: ".ghok.yaml", SHA256: contentHash([]byte("different config"))}, file("other-hook")}, TrustedAt: now}
	notes, previous := trustComparisonNotes(current, map[string]trustEntry{"unrelated": unrelated})
	if previous != nil || !reflect.DeepEqual(notes, []string{"no previous trust record for this repository"}) {
		t.Fatalf("an incidental shared include must not identify a repository: %v, %+v", notes, previous)
	}
	// Same paths with different bytes are not an overlap. Renamed files with
	// matching bytes are, and duplicate copies count as only one shared blob.
	different := fallback
	different.Files = []trustedFile{{Path: "hook", SHA256: contentHash([]byte("other hook"))}, {Path: "data", SHA256: contentHash([]byte("other data"))}}
	if previousTrustEntry(current, map[string]trustEntry{"different": different}) != nil {
		t.Fatal("path overlap must not identify a repository")
	}
	renamed := fallback
	renamed.Files = append([]trustedFile(nil), fallback.Files...)
	renamed.Files[0].Path, renamed.Files[1].Path = "renamed-hook", "renamed-data"
	if previousTrustEntry(current, map[string]trustEntry{"renamed": renamed}) == nil {
		t.Fatal("two shared blob hashes must identify the comparison record")
	}
	renamed.Files[1].SHA256 = renamed.Files[0].SHA256
	if previousTrustEntry(current, map[string]trustEntry{"duplicate": renamed}) != nil {
		t.Fatal("repeated copies of one blob must not count twice")
	}
}

func TestTrustChangedAddedRemoved(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	project := filepath.Join(repo, ".ghok.yaml")
	body, err := os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, project, string(body)+"trust: {include: [old, optional]}\n")
	writeFixture(t, filepath.Join(repo, "old"), "previous data")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, project, string(body)+"trust: {include: [new, optional]}\n")
	writeFixture(t, filepath.Join(repo, "new"), "new data")
	writeFixture(t, filepath.Join(repo, "optional"), "now present")
	writeFixture(t, filepath.Join(repo, "hook"), "changed code")
	_, _, notes, diff, err := TrustProject("", repo, env, true, false)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(notes, "\n")
	for _, want := range []string{"Changed files since trust: .ghok.yaml, hook", "Added files since trust: new, optional", "Removed files since trust: old"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	for _, want := range []string{"--- trusted/old\n+++ /dev/null\n", "-previous data\n", "--- /dev/null\n+++ current/new\n", "+new data\n", "+now present\n"} {
		if !strings.Contains(string(diff), want) {
			t.Fatalf("added/removed contents omitted %q: %s", want, diff)
		}
	}
}

func TestTrustDryRunWritesNothing(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	if _, _, _, _, err := TrustProject("", repo, env, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env["GHOK_CONFIG_DIR"]); !os.IsNotExist(err) {
		t.Fatalf("dry-run created config/cache/store: %v", err)
	}
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	type snapshot struct {
		data string
		mode fs.FileMode
		mod  time.Time
	}
	capture := func() map[string]snapshot {
		result := make(map[string]snapshot)
		err := filepath.WalkDir(env["GHOK_CONFIG_DIR"], func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var data []byte
			if !entry.IsDir() {
				data, err = os.ReadFile(path)
			}
			result[path] = snapshot{string(data), info.Mode(), info.ModTime()}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := capture()
	writeFixture(t, filepath.Join(repo, "hook"), "untrusted new content")
	if _, _, _, _, err := TrustProject("", repo, env, true, false); err != nil {
		t.Fatal(err)
	}
	if after := capture(); !reflect.DeepEqual(after, before) {
		t.Fatalf("dry-run mutated state:\nbefore=%+v\nafter=%+v", before, after)
	}
	c, _, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 {
		t.Fatalf("dry-run granted trust: %+v, %v", c, err)
	}
}
