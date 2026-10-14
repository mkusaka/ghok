package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func executable(t testing.TB, path string, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
func link(t testing.TB, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}
func testSelf(t testing.TB, root, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, data, 0755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestFindRealGhCandidates(t *testing.T) {
	root := t.TempDir()
	self := testSelf(t, root, "ghok-renamed")
	shim := filepath.Join(root, "shim", "gh")
	link(t, self, shim)
	other := executable(t, filepath.Join(root, "other", "ghok"), "exit 0")
	otherShim := filepath.Join(root, "other-shim", "gh")
	link(t, other, otherShim)
	broken := filepath.Join(root, "broken", "gh")
	link(t, "missing", broken)
	loop := filepath.Join(root, "loop", "gh")
	link(t, "again", loop)
	link(t, "gh", filepath.Join(root, "loop", "again"))
	noExec := executable(t, filepath.Join(root, "noexec", "gh"), "exit 0")
	if err := os.Chmod(noExec, 0644); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory", "gh")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	first := executable(t, filepath.Join(root, "first", "gh"), "exit 0")
	duplicate := filepath.Join(root, "duplicate", "gh")
	link(t, first, duplicate)
	last := executable(t, filepath.Join(root, "last", "gh"), "exit 0")
	paths := []string{shim, otherShim, broken, loop, noExec, directory, first, duplicate, last}
	dirs := []string{"", ".", "relative"}
	for _, p := range paths {
		dirs = append(dirs, filepath.Dir(p))
	}
	got, candidates, err := FindRealGh(DiscoverOptions{Self: self, PathEnv: strings.Join(dirs, ":")})
	if err != nil || got != first {
		t.Fatalf("path=%q err=%v", got, err)
	}
	wantReasons := []string{"relative", "relative", "relative", "ghok shim", "ghok shim", "cannot resolve", "symlink loop", "not executable", "not a regular file", "", "", ""}
	if len(candidates) != len(wantReasons) {
		t.Fatalf("candidates=%+v", candidates)
	}
	for i, reason := range wantReasons {
		if reason == "" && candidates[i].Skipped != "" || reason != "" && !strings.Contains(candidates[i].Skipped, reason) {
			t.Errorf("candidate %d=%+v want reason %q", i, candidates[i], reason)
		}
	}
	if candidates[9].Resolved != candidates[10].Resolved {
		t.Fatal("duplicate resolved installations not retained")
	}
}

func TestFindRealGhMissingAndOverride(t *testing.T) {
	root := t.TempDir()
	self := testSelf(t, root, "ghok")
	shim := filepath.Join(root, "shim", "gh")
	link(t, self, shim)
	for _, path := range []string{filepath.Dir(shim), filepath.Join(root, "missing"), ""} {
		got, candidates, err := FindRealGh(DiscoverOptions{Self: self, PathEnv: path})
		if err == nil || got != "" || !strings.Contains(err.Error(), "no real gh") || len(candidates) != 1 {
			t.Fatalf("path=%q got=%q candidates=%v err=%v", path, got, candidates, err)
		}
	}
	real := executable(t, filepath.Join(root, "real", "gh"), "exit 0")
	for _, tc := range []struct {
		path  string
		valid bool
	}{{real, true}, {shim, false}, {self, false}, {filepath.Join(root, "missing"), false}, {"gh", false}} {
		got, _, err := FindRealGh(DiscoverOptions{Self: self, Override: tc.path})
		if tc.valid && (err != nil || got != real) || !tc.valid && err == nil {
			t.Errorf("override=%s got=%q err=%v", tc.path, got, err)
		}
	}
	if _, _, err := FindRealGh(DiscoverOptions{Self: filepath.Join(root, "missing"), PathEnv: filepath.Dir(real)}); err == nil {
		t.Fatal("missing Self accepted")
	}
}

func TestHomebrewUpgrade(t *testing.T) {
	root := t.TempDir()
	self := testSelf(t, root, "ghok")
	old := executable(t, filepath.Join(root, "Cellar", "gh", "2.101.0", "bin", "gh"), "exit 0")
	shim := filepath.Join(root, "bin", "gh")
	link(t, "../Cellar/gh/2.101.0/bin/gh", shim)
	options := DiscoverOptions{Self: self, PathEnv: filepath.Dir(shim)}
	got, candidates, err := FindRealGh(options)
	if err != nil || got != shim || candidates[0].Resolved != old {
		t.Fatalf("before: %q %+v %v", got, candidates, err)
	}
	new := executable(t, filepath.Join(root, "Cellar", "gh", "2.102.0", "bin", "gh"), "exit 0")
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	link(t, "../Cellar/gh/2.102.0/bin/gh", shim)
	if err := os.RemoveAll(filepath.Dir(filepath.Dir(old))); err != nil {
		t.Fatal(err)
	}
	got, candidates, err = FindRealGh(options)
	if err != nil || got != shim || candidates[0].Resolved != new {
		t.Fatalf("after: %q %+v %v", got, candidates, err)
	}
}

func TestNextDepth(t *testing.T) {
	for initial := range MaxDepth {
		env := []string{"PATH=/fake", DepthEnv + "=" + string(rune('0'+initial)), "GH_PATH=/real/gh"}
		before := append([]string(nil), env...)
		got, depth, err := NextDepth(env)
		if err != nil || depth != initial+1 || !reflect.DeepEqual(env, before) {
			t.Fatalf("got=%v depth=%d err=%v mutated=%v", got, depth, err, env)
		}
		if got[len(got)-1] != DepthEnv+"="+string(rune('0'+depth)) {
			t.Fatalf("wrong environment %v", got)
		}
	}
	got, depth, err := NextDepth([]string{"GH_PATH=/real/gh"})
	if err != nil || depth != 1 || !reflect.DeepEqual(got, []string{"GH_PATH=/real/gh", "GHOK_DEPTH=1"}) {
		t.Fatalf("%v %d %v", got, depth, err)
	}
	for _, env := range [][]string{{"GHOK_DEPTH=4"}, {"GHOK_DEPTH=999999999999999999999"}, {"GHOK_DEPTH=-1"}, {"GHOK_DEPTH="}, {"GHOK_DEPTH=abc"}, {"GHOK_DEPTH=1", "GHOK_DEPTH=2"}} {
		if _, _, err := NextDepth(env); err == nil {
			t.Errorf("accepted %v", env)
		}
	}
}

func BenchmarkFindRealGh(b *testing.B) {
	root := b.TempDir()
	self := executable(b, filepath.Join(root, "ghok"), "exit 0")
	shim := filepath.Join(root, "shim", "gh")
	link(b, self, shim)
	real := executable(b, filepath.Join(root, "real", "gh"), "exit 0")
	options := DiscoverOptions{Self: self, PathEnv: filepath.Dir(shim) + ":" + filepath.Join(root, "missing") + ":" + filepath.Dir(real)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := FindRealGh(options); err != nil {
			b.Fatal(err)
		}
	}
}
