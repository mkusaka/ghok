package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTrustStorePersistence(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	project := filepath.Join(repo, ".ghok.yaml")
	body := "version: 1\nhooks: {pre: [{id: active, command: [./check]}, {id: disabled, command: [./check], enabled: false}]}\n"
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, project, body)
	alias := filepath.Join(root, "repo-link")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	canonical, hooks, _, _, err := TrustProject(".ghok.yaml", alias, env, false, false)
	if err != nil || canonical != project || len(hooks) != 1 || hooks[0].ID != "active" || hooks[0].Command[0] != filepath.Join(repo, "check") {
		t.Fatalf("trust via repository symlink: %s, %+v, %v", canonical, hooks, err)
	}
	storePath := filepath.Join(env["GHOK_CONFIG_DIR"], "trust.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := parseTrustStore(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(store) != 1 {
		t.Fatalf("expected one checkout record: %+v", store)
	}
	var entry trustEntry
	for key, grant := range store {
		if key != contentHash([]byte(project)) {
			t.Fatalf("trust key is not the canonical config path hash: %q", key)
		}
		entry = grant
	}
	if entry.ManifestVersion != 2 || entry.FirstPath != project || entry.ConfigPath != ".ghok.yaml" || len(entry.Files) != 1 || !entry.Files[0].Missing || entry.Files[0].Path != "check" || !reflect.DeepEqual(entry.Files[0].HookIDs, []string{"active", "disabled"}) || entry.TrustedAt.Before(before) || entry.TrustedAt.After(time.Now().UTC()) {
		t.Fatalf("persisted checkout trust: %+v", store)
	}
	for _, id := range []string{"active", "disabled"} {
		if !validDigest(entry.HookDigests[id]) || !json.Valid(entry.Definitions[id]) {
			t.Fatalf("missing per-hook digest or definition for %s: %+v", id, entry)
		}
	}
	for path, want := range map[string]os.FileMode{env["GHOK_CONFIG_DIR"]: 0o700, storePath: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions %s: %v, %v", path, info, err)
		}
	}
	// Trusting and untrusting another repository must preserve this grant.
	other := filepath.Join(root, "other")
	writeFixture(t, filepath.Join(other, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(other, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: other, command: [./other]}]}\n")
	if _, _, _, _, err := TrustProject("", other, env, false, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := UntrustProject("", other, env); err != nil {
			t.Fatal(err)
		}
	}
	remaining, notes := readTrustStore(env)
	if !reflect.DeepEqual(remaining, store) || len(notes) != 0 {
		t.Fatalf("untrust altered another grant: %+v, %v", remaining, notes)
	}
	if _, _, err := UntrustProject("", alias, env); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 2 || c.IgnoredHooks[0].Reason != "untrusted" {
		t.Fatalf("untrust: %+v, %v", c, err)
	}
}

func TestContentTrustAcrossWorktrees(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	body := "version: 1\npolicy: {mode: enforce, rules: [{id: deny, decision: deny}]}\nhooks: {pre: [{id: local, command: [./scripts/check]}]}\n"
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, repo := range []string{first, second} {
		writeFixture(t, filepath.Join(repo, ".git"), "gitdir: ../shared/worktrees/example\n")
		writeFixture(t, filepath.Join(repo, ".ghok.yaml"), body)
		writeFixture(t, filepath.Join(repo, "scripts", "check"), "#!/bin/sh\nexit 0\n")
	}
	if _, _, _, _, err := TrustProject("", first, env, false, false); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadEffective("", second, env)
	if err != nil || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 0 {
		t.Fatalf("identical worktree lost trust: %+v, %v", c, err)
	}
	writeFixture(t, filepath.Join(second, "scripts", "check"), "#!/bin/sh\nexit 1\n")
	c, _, err = LoadEffective("", second, env)
	if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].Reason != "untrusted" || c.Policy.Mode != ModeEnforce || len(c.Policy.Rules) != 1 {
		t.Fatalf("script edit must revoke hooks, not policy: %+v, %v", c, err)
	}
	writeFixture(t, filepath.Join(first, "scripts", "check"), "#!/bin/sh\nexit 2\n")
	c, _, err = LoadEffective("", first, env)
	if err != nil || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].Reason != "changed since trust" {
		t.Fatalf("edit in the trusted checkout must revoke hooks: %+v, %v", c, err)
	}
}

func TestCorruptTrustStore(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\npolicy: {mode: enforce, rules: [{id: deny, decision: deny}]}\nhooks: {pre: [{id: project, command: [./hook]}]}\n")
	storePath := filepath.Join(env["GHOK_CONFIG_DIR"], "trust.json")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"{", "null", "[]", `{"/repo/.ghok.yaml":{}}`, `{"/repo/.ghok.yaml":{"sha256":"` + strings.Repeat("a", 64) + `","trustedAt":"2026-10-15T00:00:00Z"}}`} {
		writeFixture(t, storePath, data)
		c, notes, err := LoadEffective("", repo, env)
		if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.Policy.Mode != ModeEnforce || len(c.Policy.Rules) != 1 || !strings.Contains(strings.Join(notes, "\n"), "Trust store:") {
			t.Fatalf("corrupt %q: %+v, %v, %v", data, c, notes, err)
		}
	}
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 0 {
		t.Fatalf("retrust repairs corrupt store: %+v, %v", c, err)
	}
}

func TestTrustStoreIsolatesMalformedEntries(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: project, command: [./hook]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	path := trustStorePath(env)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var mixed map[string]json.RawMessage
	if err := json.Unmarshal(good, &mixed); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"bad-key":               `{}`,
		strings.Repeat("a", 64): `{"manifestVersion":"wrong type"}`,
		strings.Repeat("b", 64): `null`,
		strings.Repeat("c", 64): `{"trustedAt":"not a timestamp"}`,
		strings.Repeat("d", 64): `["not an entry"]`,
	} {
		mixed[key] = json.RawMessage(value)
	}
	data, err := json.Marshal(mixed)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data))
	c, notes, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 1 || c.Hooks[0].ID != "project" || len(c.IgnoredHooks) != 0 {
		t.Fatalf("unrelated malformed entries revoked valid trust: %+v, %v, %v", c, notes, err)
	}
	for _, key := range []string{"bad-key", strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)} {
		prefix := key[:min(12, len(key))]
		if !strings.Contains(strings.Join(notes, "\n"), prefix) {
			t.Fatalf("missing malformed entry prefix %q in notes: %v", prefix, notes)
		}
	}
}

func FuzzTrustStore(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"/repo/.ghok.yaml":{"sha256":"bad","trustedAt":"2026-10-15T00:00:00Z"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		store, _, err := parseTrustStore(data)
		if err != nil {
			if store != nil {
				t.Fatal("corrupt store must never return partial trust")
			}
			return
		}
		for key, entry := range store {
			if entry.TrustedAt.IsZero() || !validDigest(key) {
				t.Fatalf("invalid entry granted trust: %s %+v", key, entry)
			}
		}
	})
}

func TestTrustExecutableSelection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	project := filepath.Join(repo, ".ghok.yaml")
	external := filepath.Join(root, "external")
	writeFixture(t, external, "external code")
	body := "version: 1\nhooks:\n  pre:\n  - {id: absolute, command: [" + external + "]}\n  - {id: missing, command: [./scripts/missing]}\n"
	writeFixture(t, project, body)
	writeFixture(t, filepath.Join(repo, "check"), "not the PATH executable")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, "check"), "changed but not selected")
	writeFixture(t, external, "changed external code")
	c, _, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 2 {
		t.Fatalf("external absolute files must not affect trust: %+v, %v", c, err)
	}
	writeFixture(t, filepath.Join(repo, "scripts", "missing"), "now present")
	c, _, err = LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 1 || c.Hooks[0].ID != "absolute" || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].ID != "missing" || c.IgnoredHooks[0].Reason != "changed since trust" {
		t.Fatalf("missing executable appearance must affect only its hook: %+v, %v", c, err)
	}
}

func TestTrustRejectsEscapingExecutables(t *testing.T) {
	for _, kind := range []string{"parent", "symlink", "dangling", "missing-parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
			writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
			command := "./../outside"
			if kind != "parent" {
				command = "./link"
				target := filepath.Join(root, "outside")
				if kind == "symlink" {
					writeFixture(t, target, "outside")
				}
				if err := os.Symlink(target, filepath.Join(repo, "link")); err != nil {
					t.Fatal(err)
				}
				if kind == "missing-parent" {
					command += "/missing"
				}
			}
			writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: escape, command: ["+command+"]}]}\n")
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err == nil || !strings.Contains(err.Error(), "escapes repository root") {
				t.Fatalf("escaped executable accepted: %v", err)
			}
			if c, notes, err := LoadEffective("", repo, env); err != nil || len(c.Hooks) != 0 || !strings.Contains(strings.Join(notes, "\n"), "escapes repository root") {
				t.Fatalf("escaped project must be ignored without discarding user config: %+v, %v, %v", c, notes, err)
			}
		})
	}
}

func TestProjectCommandAndIncludeRestrictions(t *testing.T) {
	for _, command := range []string{"hook", "scripts/hook", "../hook"} {
		body := []byte("version: 1\nhooks: {pre: [{id: h, command: [" + command + "]}]}\n")
		if _, err := Parse(body, ""); err != nil {
			t.Fatalf("user hook %q rejected: %v", command, err)
		}
		if _, _, err := parseProject(body, "/repo/.ghok.yaml"); err == nil {
			t.Fatalf("project hook %q accepted", command)
		}
	}
	for _, path := range []string{"/outside", "../outside", ""} {
		body := []byte(fmt.Sprintf("version: 1\ntrust: {include: [%q]}\n", path))
		if _, _, err := parseProject(body, "/repo/.ghok.yaml"); err == nil {
			t.Fatalf("nonlocal include %q accepted", path)
		}
	}
	if _, err := Parse([]byte("version: 1\ntrust: {projects: []}\n"), ""); err == nil {
		t.Fatal("removed trust.projects accepted")
	}
}

func TestTrustManifestIncludesAndExecutableBits(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	project := filepath.Join(repo, ".ghok.yaml")
	body := "version: 1\nhooks: {pre: [{id: h, command: [./hook]}]}\ntrust: {include: [data, optional, ./hook]}\n"
	writeFixture(t, project, body)
	writeFixture(t, filepath.Join(repo, "hook"), "#!/bin/sh\nexit 0\n")
	writeFixture(t, filepath.Join(repo, "data"), "reviewed data")
	if err := os.Chmod(filepath.Join(repo, "hook"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	store, _ := readTrustStore(env)
	entry := store[contentHash([]byte(project))]
	if entry.ManifestVersion != 2 || len(entry.Files) != 3 || !validDigest(entry.HookDigests["h"]) {
		t.Fatalf("manifest must contain only deduplicated dependency files: %+v", entry)
	}
	for _, file := range entry.Files {
		if file.Path == ".ghok.yaml" || !reflect.DeepEqual(file.HookIDs, []string{"h"}) {
			t.Fatalf("unexpected manifest file: %+v", file)
		}
	}
	for _, change := range []struct {
		name string
		run  func() error
	}{
		{"hook", func() error { return os.Chmod(filepath.Join(repo, "hook"), 0600) }},
		{"data", func() error { return os.WriteFile(filepath.Join(repo, "data"), []byte("new data"), 0600) }},
		{"optional", func() error { return os.WriteFile(filepath.Join(repo, "optional"), []byte("appeared"), 0600) }},
	} {
		if err := change.run(); err != nil {
			t.Fatal(err)
		}
		c, notes, err := LoadEffective("", repo, env)
		if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].Reason != "changed since trust" || !strings.Contains(strings.Join(notes, "\n"), change.name) {
			t.Fatalf("%s did not revoke and identify trust: %+v, %v, %v", change.name, c, notes, err)
		}
		if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrustManifestLimits(t *testing.T) {
	for _, kind := range []string{"count", "bytes", "directory", "fifo", "include-symlink", "config-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
			writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
			project := filepath.Join(repo, ".ghok.yaml")
			body := "version: 1\nhooks: {pre: [{id: h, command: [./hook]}]}\n"
			want := "regular file"
			switch kind {
			case "count":
				want = "256 files"
				var paths []string
				for i := range 255 {
					paths = append(paths, fmt.Sprintf("missing-%03d", i))
				}
				body += "trust: {include: [" + strings.Join(paths, ",") + "]}\n"
				writeFixture(t, project, body)
				if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
					t.Fatalf("256 files rejected: %v", err)
				}
				body = strings.Replace(body, "missing-000", "extra,missing-000", 1)
			case "bytes":
				want = "runtime.fileMaxBytes"
				writeFixture(t, filepath.Join(repo, "hook"), strings.Repeat("x", 10))
				writeFixture(t, DefaultPath(env), fmt.Sprintf("version: 1\nruntime: {fileMaxBytes: %d}\n", max(len(body), 10)))
				writeFixture(t, project, body)
				if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
					t.Fatalf("exact size limit rejected: %v", err)
				}
				writeFixture(t, filepath.Join(repo, "hook"), strings.Repeat("x", max(len(body), 10)+1))
			case "directory":
				if err := os.Mkdir(filepath.Join(repo, "hook"), 0700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				mkfifo, err := exec.LookPath("mkfifo")
				if err != nil {
					t.Skip("mkfifo not available")
				}
				if err := exec.Command(mkfifo, filepath.Join(repo, "hook")).Run(); err != nil {
					t.Fatal(err)
				}
			case "include-symlink":
				want = "escapes repository root"
				body += "trust: {include: [link/missing]}\n"
				if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(repo, "link")); err != nil {
					t.Fatal(err)
				}
			case "config-symlink":
				want = "escapes repository root"
				writeFixture(t, filepath.Join(root, "outside.yaml"), body)
				if err := os.Symlink(filepath.Join(root, "outside.yaml"), project); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "config-symlink" {
				writeFixture(t, project, body)
			}
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("trust accepted %s: %v", kind, err)
			}
			if c, notes, err := LoadEffective("", repo, env); err != nil || len(c.Hooks) != 0 || !strings.Contains(strings.Join(notes, "\n"), want) {
				t.Fatalf("loader did not isolate %s: %+v, %v, %v", kind, c, notes, err)
			}
		})
	}
}

func TestPolicyOnlyProjectSkipsManifest(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\npolicy: {rules: [{id: deny, decision: deny}]}\ntrust: {include: [.git]}\n")
	writeFixture(t, trustStorePath(env), "invalid store")
	c, notes, err := LoadEffective("", repo, env)
	if err != nil || len(c.Policy.Rules) != 1 || strings.Contains(strings.Join(notes, "\n"), "Trust store:") {
		t.Fatalf("policy-only project computed trust: %+v, %v, %v", c, notes, err)
	}
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trustStorePath(env))
	if err != nil || string(data) != "invalid store" {
		t.Fatalf("policy-only trust mutated store: %s, %v", data, err)
	}
}
