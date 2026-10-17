package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func readPathTrustFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTrustRepositoryRoot(t *testing.T) {
	repo, linked, _, _ := pathTrustFixture(t)
	sub := filepath.Join(linked, "sub", "nested")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{repo, linked, sub} {
		got, err := TrustRepositoryRoot(path, repo)
		if err != nil || got != canonicalPath(repo) {
			t.Fatalf("%s: got %s, %v", path, got, err)
		}
	}
	if got, err := TrustRepositoryRoot("sub/nested", linked); err != nil || got != canonicalPath(repo) {
		t.Fatalf("relative subdirectory: %s, %v", got, err)
	}
	// No conventional main .git directory means the checkout itself is used.
	gitdir := filepath.Join(t.TempDir(), "metadata")
	writeFixture(t, filepath.Join(gitdir, "commondir"), ".\n")
	checkout := t.TempDir()
	writeFixture(t, filepath.Join(checkout, ".git"), "gitdir: "+gitdir+"\n")
	if got, err := TrustRepositoryRoot(checkout, checkout); err != nil || got != canonicalPath(checkout) {
		t.Fatalf("checkout fallback: %s, %v", got, err)
	}
	for _, path := range []string{filepath.Join(repo, "missing"), filepath.Join(repo, ".ghok.yaml"), t.TempDir()} {
		if _, err := TrustRepositoryRoot(path, repo); err == nil {
			t.Fatalf("must reject non-directory or non-repository %s", path)
		}
	}
}

func TestUpdatePathTrustRefusals(t *testing.T) {
	for _, body := range []string{
		"version: 1\nunknown: true\n",
		"version: 1\ntrust: {directories: [owner/repo]}\n",
		"version: 1\ntrust: {include: []}\n",
		"version: 1\ntrust: {skip: []}\n",
		"version: 1\ntrust: [\n",
		"version: 1\nlogging: &trust {}\ntrust: *trust\n",
		"version: 1\ntrust: {<<: {directories: []}}\n",
		"version: 1\n<<: {trust: {directories: []}}\n",
		"version: 1\npolicy: {protectedRepositories: &paths []}\ntrust: {directories: *paths}\n",
		// Editing an anchor used elsewhere would silently modify raw semantics.
		"version: 1\ntrust: &trust {}\nlogging: *trust\n",
	} {
		t.Run(body, func(t *testing.T) {
			repo, _, _, env := pathTrustFixture(t)
			path := DefaultPath(env)
			writeFixture(t, path, body)
			_, changed, _, err := UpdatePathTrust("dir", repo, repo, env, false, false)
			if err == nil || changed || string(readPathTrustFile(t, path)) != body {
				t.Fatalf("must refuse without changes: changed=%v err=%v", changed, err)
			}
		})
	}
	repo, _, _, env := pathTrustFixture(t)
	if _, _, _, err := UpdatePathTrust("repo", ".", repo, env, false, false); err == nil || !strings.Contains(err.Error(), "run `ghok config init --scope user` first") {
		t.Fatalf("missing config: %v", err)
	}
	writeFixture(t, DefaultPath(env), "version: 1\n")
	for _, kind := range []string{"repo", "dir"} {
		for _, path := range []string{filepath.Join(repo, "missing"), filepath.Join(repo, ".ghok.yaml")} {
			if _, _, _, err := UpdatePathTrust(kind, path, repo, env, false, false); err == nil {
				t.Fatalf("%s must reject %s", kind, path)
			}
		}
	}
}

func TestUpdatePathTrustPreservesConfig(t *testing.T) {
	repo, _, _, env := pathTrustFixture(t)
	target := filepath.Join(t.TempDir(), "real.yaml")
	before := "# user heading\nversion: 1 # version comment\npolicy:\n  protectedRepositories: [org/repo] # policy comment\nhooks:\n  pre:\n    - id: user-hook\n      command: [echo, ok] # hook comment\nlogging: {enabled: false}\ntrust: # trust comment\n  directories: [] # directories comment\n"
	writeFixture(t, target, before)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	path := DefaultPath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	entry, changed, diff, err := UpdatePathTrust("dir", repo+"/", repo, env, false, false)
	if err != nil || !changed || entry != "~/src/me/repo" || len(diff) == 0 {
		t.Fatalf("edit: entry=%q changed=%v diff=%s err=%v", entry, changed, diff, err)
	}
	after := readPathTrustFile(t, path)
	for _, comment := range []string{"# user heading", "# version comment", "# policy comment", "# hook comment", "# trust comment", "# directories comment"} {
		if !bytes.Contains(after, []byte(comment)) {
			t.Fatalf("lost comment %s:\n%s", comment, after)
		}
	}
	var original, updated map[string]any
	if err := yaml.Unmarshal([]byte(before), &original); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(after, &updated); err != nil {
		t.Fatal(err)
	}
	delete(original, "trust")
	delete(updated, "trust")
	if !reflect.DeepEqual(original, updated) {
		t.Fatalf("non-trust semantics changed: before=%v after=%v", original, updated)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink replaced: %v %v", info, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("target mode changed: %v %v", info, err)
	}
	c, err := Load(path)
	if err != nil || !reflect.DeepEqual(c.Trust.Directories, []string{entry}) {
		t.Fatalf("saved config: %+v %v", c, err)
	}
}

func TestUpdatePathTrustCanonicalEntries(t *testing.T) {
	repo, linked, home, env := pathTrustFixture(t)
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	path := DefaultPath(env)
	writeFixture(t, path, "version: 1\ntrust:\n  repositories: [\"~/src/me/repo/\", \""+repo+"\", \""+alias+"/\"]\n  directories: [\"~/src\"]\n")
	entry, changed, _, err := UpdatePathTrust("repo", linked, repo, env, false, false)
	if err != nil || !changed || entry != "~/src/me/repo" {
		t.Fatalf("dedupe linked repository: %q %v %v", entry, changed, err)
	}
	c, err := Load(path)
	if err != nil || !reflect.DeepEqual(c.Trust.Repositories, []string{"~/src/me/repo/"}) || !reflect.DeepEqual(c.Trust.Directories, []string{"~/src"}) {
		t.Fatalf("canonical duplicates not removed: %+v %v", c, err)
	}
	before := readPathTrustFile(t, path)
	if _, changed, diff, err := UpdatePathTrust("repo", alias, repo, env, false, false); err != nil || changed || len(diff) != 0 || !bytes.Equal(before, readPathTrustFile(t, path)) {
		t.Fatalf("idempotent add: %v %s %v", changed, diff, err)
	}
	if _, changed, _, err := UpdatePathTrust("repo", alias+"/", repo, env, true, false); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	before = readPathTrustFile(t, path)
	if _, changed, diff, err := UpdatePathTrust("repo", repo, repo, env, true, false); err != nil || changed || len(diff) != 0 || !bytes.Equal(before, readPathTrustFile(t, path)) {
		t.Fatalf("idempotent remove: %v %s %v", changed, diff, err)
	}
	if _, changed, _, err := UpdatePathTrust("dir", "~/src/me", repo, env, false, false); err != nil || !changed {
		t.Fatalf("tilde directory: %v %v", changed, err)
	}
	outside := t.TempDir()
	if entry, changed, _, err := UpdatePathTrust("dir", outside, repo, env, false, false); err != nil || !changed || entry != canonicalPath(outside) {
		t.Fatalf("outside HOME uses absolute path: %q %v %v", entry, changed, err)
	}
	if entry, changed, _, err := UpdatePathTrust("dir", home, repo, env, false, false); err != nil || !changed || entry != "~/" {
		t.Fatalf("HOME uses tilde directory: %q %v %v", entry, changed, err)
	}
}

func TestUpdatePathTrustDryRun(t *testing.T) {
	repo, linked, _, env := pathTrustFixture(t)
	sub := filepath.Join(linked, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	path := DefaultPath(env)
	writeFixture(t, path, "version: 1\n")
	before := readPathTrustFile(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	entry, changed, diff, err := UpdatePathTrust("repo", "sub", linked, env, false, true)
	if err != nil || !changed || entry != "~/src/me/repo" || !bytes.Contains(diff, []byte("repositories:")) {
		t.Fatalf("dry-run edit: %q %v %s %v", entry, changed, diff, err)
	}
	if !bytes.Equal(before, readPathTrustFile(t, path)) {
		t.Fatal("dry-run changed config bytes")
	}
	afterInfo, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(afterInfo.ModTime()) || info.Mode() != afterInfo.Mode() {
		t.Fatalf("dry-run changed config metadata: %v", err)
	}
	afterFiles, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != len(afterFiles) {
		t.Fatalf("dry-run created files: %v", err)
	}
	if _, changed, _, err := UpdatePathTrust("repo", ".", repo, env, false, false); err != nil || !changed {
		t.Fatalf("add for dry-run removal: %v %v", changed, err)
	}
	before = readPathTrustFile(t, path)
	if _, changed, diff, err := UpdatePathTrust("repo", ".", repo, env, true, true); err != nil || !changed || len(diff) == 0 || !bytes.Equal(before, readPathTrustFile(t, path)) {
		t.Fatalf("dry-run removal: %v %s %v", changed, diff, err)
	}
}

func TestUpdatePathTrustEmptyConfig(t *testing.T) {
	for _, body := range []string{"", "{}\n", "null # keep comment\n"} {
		t.Run(body, func(t *testing.T) {
			repo, _, _, env := pathTrustFixture(t)
			path := DefaultPath(env)
			writeFixture(t, path, body)
			entry, changed, _, err := UpdatePathTrust("repo", ".", repo, env, false, false)
			if err != nil || !changed || entry != "~/src/me/repo" {
				t.Fatalf("empty config edit: %q %v %v", entry, changed, err)
			}
			c, err := Load(path)
			if err != nil || c.Version != DefaultVersion || !reflect.DeepEqual(c.Trust.Repositories, []string{entry}) {
				t.Fatalf("saved empty config: %+v %v", c, err)
			}
			if strings.Contains(body, "# keep comment") && !bytes.Contains(readPathTrustFile(t, path), []byte("# keep comment")) {
				t.Fatal("null root comment lost")
			}
		})
	}
}

func TestUpdatePathTrustSymlinkHome(t *testing.T) {
	repo, _, home, env := pathTrustFixture(t)
	homeAlias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, homeAlias); err != nil {
		t.Fatal(err)
	}
	env["HOME"] = homeAlias
	writeFixture(t, DefaultPath(env), "version: 1\n")
	if entry, changed, _, err := UpdatePathTrust("dir", "~/src/me/repo", repo, env, false, false); err != nil || !changed || entry != "~/src/me/repo" {
		t.Fatalf("canonical HOME storage: %q %v %v", entry, changed, err)
	}
}
