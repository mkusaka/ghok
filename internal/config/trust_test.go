package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
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
	body := "version: 1\nhooks: [{id: active, phase: pre, command: [./check]}, {id: disabled, phase: pre, command: [check], enabled: false}]\n"
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, project, body)
	alias := filepath.Join(root, "repo-link")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	canonical, hooks, _, err := TrustProject(".ghok.yaml", alias, env)
	if err != nil || canonical != project || len(hooks) != 1 || hooks[0].ID != "active" || hooks[0].Command[0] != filepath.Join(repo, "check") {
		t.Fatalf("trust via repository symlink: %s, %+v, %v", canonical, hooks, err)
	}
	storePath := filepath.Join(env["GHOK_CONFIG_DIR"], "trust.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	var store map[string]trustEntry
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256([]byte(body))
	entry := store[project]
	if len(store) != 1 || entry.SHA256 != hex.EncodeToString(wantDigest[:]) || entry.TrustedAt.Before(before) || entry.TrustedAt.After(time.Now().UTC()) {
		t.Fatalf("persisted exact-byte trust: %+v", store)
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
	writeFixture(t, filepath.Join(other, ".ghok.yaml"), "version: 1\n")
	if _, _, _, err := TrustProject("", other, env); err != nil {
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

func TestCorruptTrustStore(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\npolicy: {mode: enforce, rules: [{id: deny, decision: deny}]}\nhooks: [{id: project, phase: pre, command: [hook]}]\n")
	storePath := filepath.Join(env["GHOK_CONFIG_DIR"], "trust.json")
	if _, _, _, err := TrustProject("", repo, env); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	// A corrupt entry invalidates the entire store, even alongside a valid grant.
	mixed := strings.TrimSuffix(strings.TrimSpace(string(good)), "}") + ", \"/other/.ghok.yaml\": {\"sha256\":\"bad\"}}"
	for _, data := range []string{"{", "null", "[]", `{"/repo/.ghok.yaml":{}}`, mixed} {
		writeFixture(t, storePath, data)
		c, notes, err := LoadEffective("", repo, env)
		if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.Policy.Mode != ModeEnforce || len(c.Policy.Rules) != 1 || !strings.Contains(strings.Join(notes, "\n"), "Trust store:") {
			t.Fatalf("corrupt %q: %+v, %v, %v", data, c, notes, err)
		}
	}
	if _, _, _, err := TrustProject("", repo, env); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadEffective("", repo, env)
	if err != nil || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 0 {
		t.Fatalf("retrust repairs corrupt store: %+v, %v", c, err)
	}
}

func FuzzTrustStore(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"/repo/.ghok.yaml":{"sha256":"bad","trustedAt":"2026-10-15T00:00:00Z"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		store, err := parseTrustStore(data)
		if err != nil {
			if store != nil {
				t.Fatal("corrupt store must never return partial trust")
			}
			return
		}
		for path, entry := range store {
			digest, err := hex.DecodeString(entry.SHA256)
			if !filepath.IsAbs(path) || filepath.Clean(path) != path || err != nil || len(digest) != sha256.Size || entry.TrustedAt.IsZero() {
				t.Fatalf("invalid entry granted trust: %s %+v", path, entry)
			}
		}
	})
}
