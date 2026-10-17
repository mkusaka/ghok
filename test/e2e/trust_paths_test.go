//go:build unix

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

func TestPathTrustCLITransitions(t *testing.T) {
	s := newSandbox(t)
	writeTrustProject(t, s)
	cwd, err := filepath.EvalSymlinks(s.cwd)
	if err != nil {
		t.Fatal(err)
	}
	s.cwd = cwd
	if err := os.Remove(s.cfg); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "init", "--scope", "user").code(t, 0)
	before, err := os.ReadFile(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Adding subcommands must not consume the existing positional PATH/flags.
	for _, args := range [][]string{{"config", "trust", "./.ghok.yaml"}, {"config", "trust", "--hook", "project"}} {
		result := s.management(nil, args...)
		result.code(t, 0)
		if !bytes.Contains(result.stdout, []byte("Tip: trust this repository permanently: ghok config trust repo .")) || !bytes.Contains(result.stdout, []byte("ghok config trust dir "+filepath.Dir(s.cwd))) {
			t.Fatalf("missing permanent-trust hints: %s", result.stdout)
		}
	}
	dry := s.management(nil, "config", "trust", "repo", "--dry-run")
	dry.code(t, 0)
	after, err := os.ReadFile(s.cfg)
	if err != nil || !bytes.Equal(before, after) || !bytes.Contains(dry.stdout, []byte("+    - "+s.cwd)) {
		t.Fatalf("dry-run changed config or omitted diff: %s, err=%v", dry.stdout, err)
	}
	s.management(nil, "config", "trust", "repo").code(t, 0)
	after, err = os.ReadFile(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(before, []byte("\n")) {
		if strings.HasPrefix(strings.TrimSpace(string(line)), "#") && !bytes.Contains(after, bytes.TrimSpace(line)) {
			t.Fatalf("init comment lost: %s", line)
		}
	}
	duplicate := s.management(nil, "config", "trust", "repo", s.cwd+"/")
	duplicate.code(t, 0)
	if !bytes.Contains(duplicate.stdout, []byte("Already trusted")) {
		t.Fatalf("duplicate grant omitted note: %s", duplicate.stdout)
	}
	list := s.management(nil, "config", "trust", "--list")
	list.code(t, 0)
	for _, want := range []string{"project", "trust.repositories:", s.cwd, "(matches here)", "trust.directories:"} {
		if !bytes.Contains(list.stdout, []byte(want)) {
			t.Fatalf("list omitted %q: %s", want, list.stdout)
		}
	}
	covered := s.management(nil, "config", "trust", "--hook", "project")
	covered.code(t, 0)
	if bytes.Contains(covered.stdout, []byte("Tip:")) {
		t.Fatalf("covered repository got tip: %s", covered.stdout)
	}
	s.management(nil, "config", "trust", "dir", s.env["HOME"]).code(t, 0)
	all := s.management(nil, "config", "untrust", "--all")
	all.code(t, 0)
	if !bytes.Contains(all.stdout, []byte("trust.repositories/directories unchanged")) {
		t.Fatalf("untrust --all omitted retained entries: %s", all.stdout)
	}
	c, err := config.Load(s.cfg)
	if err != nil || !reflect.DeepEqual(c.Trust.Repositories, []string{s.cwd}) || !reflect.DeepEqual(c.Trust.Directories, []string{"~/"}) {
		t.Fatalf("untrust --all changed path trust: %+v, err=%v", c, err)
	}
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
	for _, args := range [][]string{{"config", "validate"}, {"doctor", "--json"}, {"explain", "--", "version"}} {
		result := s.management(nil, args...)
		result.code(t, 0)
		if !bytes.Contains(result.stdout, []byte("Project hooks and transforms run without per-hook trust (user trust.repositories")) {
			t.Fatalf("%v omitted path-trust note: %s", args, result.stdout)
		}
	}
	s.gh(nil, "version").code(t, 0)
	s.hookInput("project")
	dry = s.management(nil, "config", "untrust", "repo", "--dry-run")
	dry.code(t, 0)
	c, err = config.Load(s.cfg)
	if err != nil || !reflect.DeepEqual(c.Trust.Repositories, []string{s.cwd}) || !bytes.Contains(dry.stdout, []byte("Dry run; would remove")) {
		t.Fatalf("untrust dry-run changed entries: %+v %v %s", c, err, dry.stdout)
	}
	s.management(nil, "config", "untrust", "repo").code(t, 0)
	absent := s.management(nil, "config", "untrust", "repo")
	absent.code(t, 0)
	if !bytes.Contains(absent.stdout, []byte("Already absent")) {
		t.Fatalf("idempotent removal omitted note: %s", absent.stdout)
	}
	s.management(nil, "config", "untrust", "dir", s.env["HOME"]).code(t, 0)
	c, err = config.Load(s.cfg)
	if err != nil || len(c.Trust.Repositories)+len(c.Trust.Directories) != 0 {
		t.Fatalf("entries not removed: %+v %v", c, err)
	}
}

func TestPathTrustCLISymlinkAndDirectoryTip(t *testing.T) {
	s := newSandbox(t)
	writeTrustProject(t, s)
	cwd, err := filepath.EvalSymlinks(s.cwd)
	if err != nil {
		t.Fatal(err)
	}
	s.cwd = cwd
	target := filepath.Join(s.root, "dotfiles.yaml")
	original := []byte("# dotfiles comment\nversion: 1\npolicy: {mode: observe} # keep mode\n")
	if err := os.WriteFile(target, original, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.cfg); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust", "dir", s.cwd).code(t, 0)
	info, err := os.Lstat(s.cfg)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink replaced: %v %v", info, err)
	}
	info, err = os.Stat(target)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("target mode changed: %v %v", info, err)
	}
	body, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(body, []byte("# dotfiles comment")) || !bytes.Contains(body, []byte("# keep mode")) {
		t.Fatalf("comments lost: %s %v", body, err)
	}
	covered := s.management(nil, "config", "trust", "./.ghok.yaml")
	covered.code(t, 0)
	if bytes.Contains(covered.stdout, []byte("Tip:")) {
		t.Fatalf("directory-covered repository got tip: %s", covered.stdout)
	}
	list := s.management(nil, "config", "trust", "--list")
	list.code(t, 0)
	if !bytes.Contains(list.stdout, []byte("trust.directories:\n  \""+s.cwd+"\" (matches here)")) {
		t.Fatalf("directory match not marked: %s", list.stdout)
	}
}

func TestPathTrustCLIRepositoryResolution(t *testing.T) {
	s := newSandbox(t)
	main := filepath.Join(s.env["HOME"], "src", "repo")
	gitdir := filepath.Join(main, ".git", "worktrees", "linked")
	linked := filepath.Join(s.root, "linked")
	for _, dir := range []string{gitdir, filepath.Join(main, "sub"), filepath.Join(linked, "sub")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitdir+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{filepath.Join(main, "sub"), filepath.Join(linked, "sub")} {
		s.cwd = cwd
		s.management(nil, "config", "trust", "repo").code(t, 0)
		c, err := config.Load(s.cfg)
		if err != nil || !reflect.DeepEqual(c.Trust.Repositories, []string{"~/src/repo"}) {
			t.Fatalf("%s did not store main root: %+v %v", cwd, c, err)
		}
		s.management(nil, "config", "untrust", "repo").code(t, 0)
	}
	if err := os.WriteFile(filepath.Join(linked, ".ghok.yaml"), []byte("version: 1\nhooks: {pre: [{id: gate, command: [./gate], onError: allow}]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := s.management(nil, "config", "trust")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("ghok config trust dir ~/src")) {
		t.Fatalf("linked-worktree tip must name main parent: %s", result.stdout)
	}
}

func TestPathTrustCLIErrors(t *testing.T) {
	s := newSandbox(t)
	writeTrustProject(t, s)
	for _, args := range [][]string{
		{"config", "trust", "repo", "--hook", "project"},
		{"config", "trust", "repo", "--list"},
		{"config", "untrust", "dir", s.cwd, "--all"},
		{"config", "trust", "dir"},
		{"config", "trust", "repo", ".", "."},
	} {
		s.management(nil, args...).code(t, 2)
	}
	s.management(nil, "config", "trust", "dir", filepath.Join(s.root, "nonexistent")).code(t, 1)
	if err := os.Remove(s.cfg); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"trust", "untrust"} {
		result := s.management(nil, "config", command, "repo")
		result.code(t, 1)
		if !bytes.Contains(result.stderr, []byte("run `ghok config init --scope user` first")) {
			t.Fatalf("missing config instruction: %s", result.stderr)
		}
		if _, err := os.Stat(s.cfg); !os.IsNotExist(err) {
			t.Fatalf("missing config created: %v", err)
		}
	}
	invalid := []byte("version: 1\nunknown: true\n")
	if err := os.WriteFile(s.cfg, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust", "repo").code(t, 1)
	body, err := os.ReadFile(s.cfg)
	if err != nil || !bytes.Equal(body, invalid) {
		t.Fatalf("invalid config changed: %s %v", body, err)
	}
}
