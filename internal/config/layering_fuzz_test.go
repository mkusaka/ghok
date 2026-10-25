package config

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"
)

// FuzzLayeredConfig checks the invariants of user → .ghok.yaml → .ghok.local.yaml
// layering for arbitrary YAML: overlays only tighten policy, never set trust,
// runtime or logging, project hooks stay inactive without user trust, invalid
// config fails closed when declared enforce or GHOK_FAIL_CLOSED=1, and every
// file ghok writes under the config dir is private.
func FuzzLayeredConfig(f *testing.F) {
	hook := "hooks:\n  pre:\n  - {id: p, command: [./check]}\n"
	for _, s := range [][3]string{
		{"version: 1\npolicy: {mode: enforce}\n", "version: 1\npolicy: {mode: observe}\n", ""},
		{"version: 1\npolicy: {unknownMutation: deny, unknownInput: deny, protectedUnknown: deny}\n", "version: 1\npolicy: {unknownMutation: allow, unknownInput: allow, protectedUnknown: allow}\n", "version: 1\npolicy: {unknownMutation: audit}\n"},
		{"version: 1\npolicy:\n  rules: [{id: u, match: {operation: repo.delete}, decision: deny}]\n  protectedRepositories: [o/r]\n", "version: 1\npolicy:\n  mode: enforce\n  rules: [{id: p, match: {operation: pr.merge.*}, decision: audit}]\n", "version: 1\npolicy: {protectedRepositories: [o/*]}\n"},
		{"version: 1\n", "version: 1\ntrust: {repositories: [\"~/repo\"]}\n" + hook, ""},
		{"version: 1\n", "version: 1\ntrust: {directories: [\"~\"]}\n", "version: 1\ntrust: {include: [x]}\n"},
		{"version: 1\n", "version: 1\nruntime: {hookConcurrency: 1}\n", "version: 1\nlogging: {enabled: false}\n"},
		{"version: 1\n", "version: 1\n" + hook, "version: 1\nhooks:\n  pre:\n  - {id: l, command: [./check], files: [x]}\n"},
		{"version: 1\n", "version: 1\nhooks:\n  pre:\n  - {id: bare, command: [check]}\n", "version: 1\n" + hook},
		{"version: 1\ntrust: {repositories: [\"~/repo\"]}\n", "version: 1\n" + hook + "transforms:\n  pre:\n  - {id: t, command: [./t]}\n", ""},
		{"version: 1\ntrust: {include: [x]}\n", "version: 1\n", ""},
		{"version: 1\npolicy: {mode: enforce}\nbogus: 1\n", "", ""},
		{"version: 1\npolicy: {mode: observe}\nbogus: 1\n", "version: 1\npolicy: {mode: enforce}\n", ""},
		{"", "version: 1\n<<: {trust: {repositories: [\"~/repo\"]}}\n" + hook, ""},
		{"version: 1\n", "version: 1\npolicy: {mode: enforce}\n", "version: 1\npolicy: {mode: observe}\n"},
	} {
		f.Add(s[0], s[1], s[2])
	}
	// No git on PATH: local configs are never "tracked", and no exec per input.
	f.Setenv("PATH", f.TempDir())
	f.Fuzz(func(t *testing.T, user, project, local string) {
		checkLayeredConfig(t, user, project, local)
	})
}

// TestLayeredConfigInvariantsLoadOverlays guards against a vacuous fuzz target:
// with a trusted repository both overlays must load and project hooks run.
func TestLayeredConfigInvariantsLoadOverlays(t *testing.T) {
	base, eff := checkLayeredConfig(t,
		"version: 1\npolicy: {mode: enforce}\ntrust: {repositories: [\"~/repo\"]}\n",
		"version: 1\npolicy:\n  rules: [{id: p, match: {operation: pr.merge.*}, decision: deny}]\nhooks:\n  pre:\n  - {id: p, command: [./check]}\n",
		"version: 1\npolicy:\n  rules: [{id: l, match: {operation: repo.delete}, decision: deny}]\n")
	if eff == nil || len(eff.Diagnostics) != 0 || len(eff.Policy.Rules) != len(base.Policy.Rules)+2 || len(eff.Hooks) != 1 || len(eff.IgnoredHooks) != 0 {
		t.Fatalf("overlays did not load: %+v", eff)
	}
}

func checkLayeredConfig(t *testing.T, user, project, local string) (base, eff *Config) {
	t.Helper()
	defer syscall.Umask(syscall.Umask(0)) // prove modes, not the inherited umask
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	userPath, projectPath, localPath := DefaultPath(env), filepath.Join(repo, ".ghok.yaml"), filepath.Join(repo, LocalName)
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	if user == "" {
		if _, err := Init(ScopeUser, repo, env); err != nil {
			t.Fatal(err)
		}
	} else {
		writeFixture(t, userPath, user)
	}
	if project != "" {
		writeFixture(t, projectPath, project)
	}
	if local != "" {
		writeFixture(t, localPath, local)
	}

	base, _, baseErr := LoadEffective("", root, env) // HOME stops project discovery
	eff, _, err = LoadEffective("", repo, env)
	if baseErr != nil {
		if err == nil {
			t.Fatalf("invalid user config accepted with overlays: %v", baseErr)
		}
		if m := lenient(user); m != nil && dig(m, "policy", "mode") == ModeEnforce && !FailClosed(err, env) {
			t.Fatalf("invalid enforce user config fails open: %v", err)
		}
		if !FailClosed(err, map[string]string{"GHOK_FAIL_CLOSED": "1"}) {
			t.Fatalf("GHOK_FAIL_CLOSED=1 fails open: %v", err)
		}
		return nil, nil
	}
	if err != nil {
		t.Fatalf("invalid overlay failed open user config: %v", err)
	}

	rank := map[string]int{ModeObserve: 0, ModeEnforce: 1, UnknownAllow: 0, UnknownAudit: 1, UnknownDeny: 2}
	for _, pair := range [][2]string{
		{base.Policy.Mode, eff.Policy.Mode},
		{base.Policy.UnknownMutation, eff.Policy.UnknownMutation},
		{base.Policy.UnknownInput, eff.Policy.UnknownInput},
		{base.Policy.ProtectedUnknown, eff.Policy.ProtectedUnknown},
	} {
		if rank[pair[1]] < rank[pair[0]] {
			t.Fatalf("overlay weakened policy %q -> %q", pair[0], pair[1])
		}
	}
	if !hasPrefix(eff.Policy.Rules, base.Policy.Rules) {
		t.Fatalf("overlay dropped or changed user rules: %+v", eff.Policy.Rules)
	}
	if !hasPrefix(eff.Policy.ProtectedRepositories, base.Policy.ProtectedRepositories) {
		t.Fatalf("overlay dropped protected repositories: %v", eff.Policy.ProtectedRepositories)
	}
	if !reflect.DeepEqual(eff.Trust, base.Trust) || !reflect.DeepEqual(eff.Runtime, base.Runtime) || !reflect.DeepEqual(eff.Logging, base.Logging) {
		t.Fatalf("overlay changed user-only settings: trust %+v runtime %+v logging %+v", eff.Trust, eff.Runtime, eff.Logging)
	}
	var sources []string
	for _, h := range eff.Hooks {
		sources = append(sources, h.Source)
	}
	for _, tr := range eff.Transforms {
		sources = append(sources, tr.Source)
	}
	for i, h := range base.Hooks {
		if i >= len(eff.Hooks) || eff.Hooks[i].ID != h.ID || eff.Hooks[i].Source != h.Source {
			t.Fatalf("overlay dropped or reordered user hooks: %v", sources)
		}
	}
	for i, tr := range base.Transforms {
		if i >= len(eff.Transforms) || eff.Transforms[i].ID != tr.ID || eff.Transforms[i].Source != tr.Source {
			t.Fatalf("overlay dropped or reordered user transforms: %v", sources)
		}
	}
	if _, entry := TrustMatch(base.Trust, repo, env); entry == "" {
		for _, source := range sources {
			if source != userPath && source != localPath {
				t.Fatalf("untrusted project hook or transform active from %s", source)
			}
		}
	}

	diagnostics := strings.Join(eff.Diagnostics, "\n")
	if m := lenient(project); m != nil && (dig(m, "trust", "repositories") != nil || dig(m, "trust", "directories") != nil || m["runtime"] != nil || m["logging"] != nil) &&
		!strings.Contains(diagnostics, "project config ignored") {
		t.Fatalf("project setting user-only keys was not ignored: %v", eff.Diagnostics)
	}
	if m := lenient(local); m != nil && (m["trust"] != nil || m["runtime"] != nil || m["logging"] != nil) &&
		!strings.Contains(diagnostics, "local config ignored") {
		t.Fatalf("local setting user-only keys was not ignored: %v", eff.Diagnostics)
	}
	strict := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": env["GHOK_CONFIG_DIR"], "GHOK_FAIL_CLOSED": "1"}
	if strings.Contains(diagnostics, "ignored:") {
		if _, _, err := LoadEffective("", repo, strict); err == nil {
			t.Fatalf("GHOK_FAIL_CLOSED=1 accepted an ignored overlay: %v", eff.Diagnostics)
		}
	}

	if project != "" {
		_, _, _, _, _ = TrustProject("", repo, env, false, false) // writes the trust store and blobs
		if _, _, err := LoadEffective("", repo, env); err != nil {
			t.Fatal(err)
		}
	}
	err = filepath.WalkDir(env["GHOK_CONFIG_DIR"], func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want no group/other access", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return base, eff
}

// lenient decodes YAML independently of the loader, following merge keys.
func lenient(s string) map[string]any {
	var m map[string]any
	if yaml.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

func dig(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		next, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = next[k]
	}
	return v
}

func hasPrefix[T any](s, prefix []T) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if !reflect.DeepEqual(s[i], prefix[i]) {
			return false
		}
	}
	return true
}
