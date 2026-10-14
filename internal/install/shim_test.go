package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("GHOK_INSTALL_TEST_HELPER") == "1" {
		if os.Getenv("GHOK_PROBE") == "1" {
			fmt.Print("ghok-probe\n")
			os.Exit(0)
		}
		os.Exit(9)
	}
	os.Exit(m.Run())
}

func enableFixture(t *testing.T, name string) EnableOptions {
	t.Helper()
	root := t.TempDir()
	self := testSelf(t, root, name)
	real := executable(t, filepath.Join(root, "real", "gh"), "printf 'gh version 2.102.0 (2026-10-01)\\n'")
	bin := filepath.Join(root, "local", "bin")
	return EnableOptions{BinDir: bin, Self: self, StateDir: filepath.Join(root, "state"), PathEnv: bin + ":" + filepath.Dir(real)}
}

func TestEnableDisableLifecycle(t *testing.T) {
	for _, name := range []string{"ghok", "ghok-1.2"} {
		t.Run(name, func(t *testing.T) {
			o := enableFixture(t, name)
			r, err := Enable(o)
			if err != nil || !r.Created || !r.OnPath || r.FixHint != "" || r.RealGh != filepath.Join(filepath.Dir(o.Self), "real", "gh") {
				t.Fatalf("enable=%+v err=%v", r, err)
			}
			info, err := os.Stat(filepath.Join(o.StateDir, "shims.json"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("state info=%v err=%v", info, err)
			}
			if err := ProbeShim(r.ShimPath, []string{"GHOK_INSTALL_TEST_HELPER=1", "GHOK_PROBE=old", "HOME=" + t.TempDir(), "PATH=" + o.PathEnv}); err != nil {
				t.Fatal(err)
			}
			again, err := Enable(o)
			if err != nil || again.Created {
				t.Fatalf("repeat=%+v err=%v", again, err)
			}
			disable := DisableOptions{BinDir: o.BinDir, Self: o.Self, StateDir: o.StateDir}
			removed, err := Disable(disable)
			if err != nil || len(removed) != 1 || removed[0] != r.ShimPath {
				t.Fatalf("disable=%v err=%v", removed, err)
			}
			removed, err = Disable(disable)
			if err != nil || len(removed) != 0 {
				t.Fatalf("repeat disable=%v err=%v", removed, err)
			}
			if _, err := os.Stat(again.RealGh); err != nil {
				t.Fatalf("real gh touched: %v", err)
			}
		})
	}
}

func TestEnableUpgradeAndMovedExecutable(t *testing.T) {
	o := enableFixture(t, "ghok-1.2")
	first, err := Enable(o)
	if err != nil {
		t.Fatal(err)
	}
	old := o.Self
	o.Self = testSelf(t, filepath.Dir(old), "ghok-1.3")
	// The old target is still executable and renamed: state must exclude it
	// from real-gh discovery during the upgrade.
	updated, err := Enable(o)
	if err != nil || updated.Created || updated.RealGh != first.RealGh {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	target, err := os.Readlink(first.ShimPath)
	if err != nil || target != o.Self {
		t.Fatalf("target=%q err=%v", target, err)
	}
	if err := os.Remove(o.Self); err != nil {
		t.Fatal(err)
	}
	// State still establishes ownership after the binary has been removed.
	owned, err := IsOwnedShim(first.ShimPath, o.Self, o.StateDir)
	if err != nil || !owned {
		t.Fatalf("owned=%t err=%v", owned, err)
	}
	removed, err := Disable(DisableOptions{BinDir: o.BinDir, Self: o.Self, StateDir: o.StateDir})
	if err != nil || len(removed) != 1 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}

func TestForeignCollisionAndDisablePreservesReplacement(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "broken-symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			o := enableFixture(t, "ghok")
			shim := filepath.Join(o.BinDir, "gh")
			switch kind {
			case "file":
				executable(t, shim, "printf foreign")
			case "symlink":
				link(t, executable(t, filepath.Join(filepath.Dir(o.Self), "foreign"), "printf foreign"), shim)
			case "broken-symlink":
				link(t, "/nonexistent-ghok-install-test", shim)
			case "directory":
				if err := os.MkdirAll(shim, 0755); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(shim)
			beforeTarget, _ := os.Readlink(shim)
			if _, err := Enable(o); err == nil || !strings.Contains(err.Error(), "not managed") {
				t.Fatalf("error=%v", err)
			}
			removed, err := Disable(DisableOptions{BinDir: o.BinDir, Self: o.Self, StateDir: o.StateDir})
			if err != nil || len(removed) != 0 {
				t.Fatalf("removed=%v err=%v", removed, err)
			}
			after, _ := os.ReadFile(shim)
			afterTarget, _ := os.Readlink(shim)
			if string(before) != string(after) || beforeTarget != afterTarget {
				t.Fatal("foreign collision changed")
			}
			if _, err := os.Lstat(shim); err != nil {
				t.Fatal("foreign collision removed")
			}
		})
	}
	o := enableFixture(t, "ghok")
	r, err := Enable(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.ShimPath); err != nil {
		t.Fatal(err)
	}
	foreign := executable(t, filepath.Join(filepath.Dir(o.Self), "foreign"), "printf foreign")
	link(t, foreign, r.ShimPath)
	if owned, err := IsOwnedShim(r.ShimPath, o.Self, o.StateDir); err != nil || owned {
		t.Fatalf("stale state owns foreign replacement: %t %v", owned, err)
	}
	if _, err := Enable(o); err == nil {
		t.Fatal("foreign replacement overwritten")
	}
	if removed, err := Disable(DisableOptions{BinDir: o.BinDir, Self: o.Self, StateDir: o.StateDir}); err != nil || len(removed) != 0 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if target, _ := os.Readlink(r.ShimPath); target != foreign {
		t.Fatal("foreign replacement changed")
	}
}

func TestOwnershipWithoutState(t *testing.T) {
	root := t.TempDir()
	self := testSelf(t, root, "renamed")
	shim := filepath.Join(root, "gh")
	link(t, "renamed", shim)
	if owned, err := IsOwnedShim(shim, self, ""); err != nil || !owned {
		t.Fatalf("SameFile ownership: %t %v", owned, err)
	}
	other := testSelf(t, root, "ghok")
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	link(t, other, shim)
	if owned, err := IsOwnedShim(shim, self, ""); err != nil || !owned {
		t.Fatalf("basename ownership: %t %v", owned, err)
	}
	if owned, err := IsOwnedShim(self, self, ""); err != nil || owned {
		t.Fatalf("regular file owned: %t %v", owned, err)
	}
}

func TestPathPrecedenceAndSymlinkedBinDir(t *testing.T) {
	o := enableFixture(t, "ghok")
	realDir := filepath.Join(filepath.Dir(o.Self), "real")
	for _, tc := range []struct {
		path   string
		onPath bool
		detail string
	}{{realDir + ":" + o.BinDir, true, "precedes"}, {realDir, false, "not on PATH"}} {
		o.PathEnv = tc.path
		r, err := Enable(o)
		if err != nil || r.OnPath != tc.onPath || !strings.Contains(r.Precedence, tc.detail) || !strings.HasPrefix(r.FixHint, "export PATH=") {
			t.Fatalf("%+v err=%v", r, err)
		}
	}
	alias := filepath.Join(filepath.Dir(o.Self), "alias-bin")
	link(t, o.BinDir, alias)
	o.BinDir = alias
	o.PathEnv = alias + ":" + realDir
	r, err := Enable(o)
	if err != nil || r.Created || !r.OnPath || r.FixHint != "" {
		t.Fatalf("alias=%+v err=%v", r, err)
	}
	removed, err := Disable(DisableOptions{BinDir: alias, Self: o.Self, StateDir: o.StateDir})
	if err != nil || len(removed) != 1 {
		t.Fatalf("alias disable=%v err=%v", removed, err)
	}
}

func TestEnableFailureDoesNotInstall(t *testing.T) {
	o := enableFixture(t, "ghok")
	o.PathEnv = o.BinDir
	if _, err := Enable(o); err == nil {
		t.Fatal("enabled without real gh")
	}
	if _, err := os.Lstat(filepath.Join(o.BinDir, "gh")); !os.IsNotExist(err) {
		t.Fatalf("shim installed despite failed discovery: %v", err)
	}
	o.PathEnv = filepath.Join(filepath.Dir(o.Self), "real")
	if err := os.MkdirAll(o.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.StateDir, "shims.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(o); err == nil {
		t.Fatal("corrupt ownership ignored")
	}
	if _, err := os.Lstat(filepath.Join(o.BinDir, "gh")); !os.IsNotExist(err) {
		t.Fatalf("shim installed despite invalid state: %v", err)
	}
}

func TestProbeRejectsWrongOutputAndExit(t *testing.T) {
	root := t.TempDir()
	for _, body := range []string{"printf 'ghok-probe'", "printf 'ghok-probe\\n'; exit 1", "printf 'foreign\\n'"} {
		path := executable(t, filepath.Join(root, "gh"), body)
		if err := ProbeShim(path, []string{"HOME=" + root, "PATH=/nonexistent"}); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

// A Homebrew install resolves to a versioned Cellar path that `brew upgrade`
// deletes; the shim must point at the stable opt path instead.
func TestEnableHomebrewShimSurvivesUpgrade(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install := func(version string) string {
		cellar := filepath.Join(root, "Cellar", "ghok", version, "bin")
		if err := os.MkdirAll(cellar, 0755); err != nil {
			t.Fatal(err)
		}
		self := filepath.Join(cellar, "ghok")
		if err := os.Rename(testSelf(t, root, "ghok-build"), self); err != nil {
			t.Fatal(err)
		}
		opt := filepath.Join(root, "opt", "ghok")
		_ = os.Remove(opt)
		if err := os.MkdirAll(filepath.Dir(opt), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "Cellar", "ghok", version), opt); err != nil {
			t.Fatal(err)
		}
		return self
	}
	self := install("1.0.0")
	real := executable(t, filepath.Join(root, "real", "gh"), "true")
	bin := filepath.Join(root, "local", "bin")
	o := EnableOptions{BinDir: bin, Self: self, StateDir: filepath.Join(root, "state"), PathEnv: bin + ":" + filepath.Dir(real)}
	r, err := Enable(o)
	if err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(root, "opt", "ghok", "bin", "ghok")
	if target, err := os.Readlink(r.ShimPath); err != nil || target != stable {
		t.Fatalf("shim target %q err=%v, want %q", target, err, stable)
	}
	// brew upgrade: new Cellar version, opt repointed, old version removed.
	newSelf := install("1.1.0")
	if err := os.RemoveAll(filepath.Join(root, "Cellar", "ghok", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.ShimPath); err != nil {
		t.Fatalf("shim broken after upgrade: %v", err)
	}
	o.Self = newSelf
	again, err := Enable(o)
	if err != nil || again.Created {
		t.Fatalf("re-install after upgrade = %+v err=%v", again, err)
	}
	if owned, err := IsOwnedShim(r.ShimPath, newSelf, o.StateDir); err != nil || !owned {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	if removed, err := Disable(DisableOptions{BinDir: bin, Self: newSelf, StateDir: o.StateDir}); err != nil || len(removed) != 1 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}
