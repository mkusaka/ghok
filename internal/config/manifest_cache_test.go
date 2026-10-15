package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manifestCacheFixture(t *testing.T) (string, map[string]string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\npolicy: {mode: observe}\nhooks: [{id: project, phase: pre, command: [./hook]}]\n")
	writeFixture(t, filepath.Join(repo, "hook"), "reviewed hook\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(repo, "hook"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifestCacheMetadata(info); !ok {
		t.Skip("safe persistent stat cache unavailable on this platform")
	}
	return repo, env, filepath.Join(env["GHOK_CONFIG_DIR"], "state", "manifest-cache.json")
}

func TestManifestCacheWarmPrivateAndUnchanged(t *testing.T) {
	repo, env, cache := manifestCacheFixture(t)
	stamp := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(cache, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		c, notes, err := LoadEffective("", repo, env)
		if err != nil || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 0 {
			t.Fatalf("warm trusted load: %+v, %v, %v", c, notes, err)
		}
	}
	after, err := os.Stat(cache)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("warm hit rewrote cache: %v, %v, %v", before, after, err)
	}
	for path, want := range map[string]os.FileMode{cache: 0o600, filepath.Dir(cache): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("private cache %s: %v, %v", path, info, err)
		}
	}
	if len(data) > maxManifestCacheBytes {
		t.Fatalf("cache exceeds byte bound: %d", len(data))
	}
	entries, err := os.ReadDir(filepath.Dir(cache))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(cache) {
		t.Fatalf("atomic write left temporary files: %v, %v", entries, err)
	}
}

func TestManifestCacheRetainsOtherProjects(t *testing.T) {
	repo, env, _ := manifestCacheFixture(t)
	other := filepath.Join(filepath.Dir(repo), "other")
	writeFixture(t, filepath.Join(other, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(other, ".ghok.yaml"), "version: 1\nhooks: [{id: other, phase: pre, command: [./hook]}]\n")
	writeFixture(t, filepath.Join(other, "hook"), "other hook\n")
	if _, _, err := LoadEffective("", other, env); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{repo, other} {
		path, err := filepath.EvalSymlinks(filepath.Join(root, "hook"))
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if hash, ok := readManifestCache(env).lookup(path, info); !ok || hash != contentHash(data) {
			t.Fatalf("loading another project discarded cached hash for %s: %q, %v", path, hash, ok)
		}
	}
}

func TestManifestCacheEvictsOldest(t *testing.T) {
	repo, env, _ := manifestCacheFixture(t)
	cache := readManifestCache(env)
	var oldest string
	var entry manifestCacheEntry
	for path, cached := range cache.entries {
		oldest, entry = path, cached
	}
	entry.Updated = 1
	cache.entries[oldest] = entry
	for i := 1; i < maxManifestCacheEntries; i++ {
		entry.Updated = int64(i + 1)
		cache.entries[filepath.Join(repo, fmt.Sprintf("older-%04d", i))] = entry
	}
	writeFixture(t, filepath.Join(repo, "new-hook"), "new hook\n")
	newest, err := filepath.EvalSymlinks(filepath.Join(repo, "new-hook"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(newest)
	if err != nil {
		t.Fatal(err)
	}
	hash := contentHash([]byte("new hook\n"))
	cache.remember(newest, info, info, hash)
	cache.save()
	cache = readManifestCache(env)
	if len(cache.entries) != maxManifestCacheEntries {
		t.Fatalf("cache has %d entries, want %d", len(cache.entries), maxManifestCacheEntries)
	}
	if _, ok := cache.entries[oldest]; ok {
		t.Fatal("oldest hash was not evicted")
	}
	if _, ok := cache.entries[filepath.Join(repo, "older-0001")]; !ok {
		t.Fatal("a newer hash was evicted before the oldest")
	}
	if got, ok := cache.lookup(newest, info); !ok || got != hash {
		t.Fatalf("new hash lost during eviction: %q, %v", got, ok)
	}
}

func TestManifestCacheInvalidatesChangedFiles(t *testing.T) {
	for _, change := range []string{"bytes", "preserved-mtime", "replacement-inode", "executable", "yaml"} {
		t.Run(change, func(t *testing.T) {
			repo, env, _ := manifestCacheFixture(t)
			hook := filepath.Join(repo, "hook")
			before, err := os.Stat(hook)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "bytes", "preserved-mtime":
				writeFixture(t, hook, "modified hook\n")
				if change == "preserved-mtime" {
					if err := os.Chtimes(hook, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
				}
			case "replacement-inode":
				replacement := filepath.Join(repo, "replacement")
				writeFixture(t, replacement, "modified hook\n")
				if err := os.Chtimes(replacement, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, hook); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Chmod(hook, 0o700); err != nil {
					t.Fatal(err)
				}
			case "yaml":
				project := filepath.Join(repo, ".ghok.yaml")
				data, err := os.ReadFile(project)
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, project, strings.Replace(string(data), "observe", "enforce", 1))
			}
			c, notes, err := LoadEffective("", repo, env)
			if change == "yaml" {
				if err != nil || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 0 {
					t.Fatalf("unrelated policy edit revoked hook trust: %+v, %v, %v", c, notes, err)
				}
			} else if err != nil || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].Reason != "changed since trust" {
				t.Fatalf("%s retained cached trust: %+v, %v, %v", change, c, notes, err)
			}
			if change == "yaml" && c.Policy.Mode != ModeEnforce {
				t.Fatalf("cache supplied stale YAML policy: %+v", c.Policy)
			}
		})
	}
}

func TestManifestCacheRetainsManifestValidation(t *testing.T) {
	for _, change := range []string{"byte-limit", "directory", "escaping-symlink"} {
		t.Run(change, func(t *testing.T) {
			repo, env, _ := manifestCacheFixture(t)
			hook := filepath.Join(repo, "hook")
			want := "regular file"
			switch change {
			case "byte-limit":
				want = "runtime.fileMaxBytes"
				info, err := os.Stat(filepath.Join(repo, ".ghok.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, DefaultPath(env), fmt.Sprintf("version: 1\nruntime: {fileMaxBytes: %d}\n", info.Size()))
				writeFixture(t, hook, strings.Repeat("x", int(info.Size())+1))
			case "directory":
				if err := os.Remove(hook); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(hook, 0o700); err != nil {
					t.Fatal(err)
				}
			case "escaping-symlink":
				want = "escapes repository root"
				outside := filepath.Join(filepath.Dir(repo), "outside")
				writeFixture(t, outside, "reviewed hook\n")
				if err := os.Remove(hook); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, hook); err != nil {
					t.Fatal(err)
				}
			}
			c, notes, err := LoadEffective("", repo, env)
			if err != nil || len(c.Hooks) != 0 || !strings.Contains(strings.Join(notes, "\n"), want) {
				t.Fatalf("cache bypassed %s: %+v, %v, %v", change, c, notes, err)
			}
		})
	}
}

func TestManifestCacheIgnoresCorruptAndUnboundedState(t *testing.T) {
	for _, corruption := range []string{"syntax", "digest", "version", "bytes", "entries", "mode"} {
		t.Run(corruption, func(t *testing.T) {
			repo, env, cache := manifestCacheFixture(t)
			data, err := os.ReadFile(cache)
			if err != nil {
				t.Fatal(err)
			}
			var stored manifestCacheData
			if err := json.Unmarshal(data, &stored); err != nil {
				t.Fatal(err)
			}
			// Matching metadata with a wrong hash makes accidental acceptance
			// visible as lost hook authorization, rather than merely a cache miss.
			var example manifestCacheEntry
			for path, entry := range stored.Files {
				entry.SHA256 = contentHash([]byte("unreviewed"))
				if corruption == "digest" {
					entry.SHA256 = "invalid"
				}
				stored.Files[path] = entry
				example = entry
			}
			if corruption == "version" {
				stored.Version++
			}
			if corruption == "entries" {
				for i := range maxManifestCacheEntries + 1 {
					stored.Files[filepath.Join(repo, fmt.Sprintf("extra-%03d", i))] = example
				}
			}
			data, err = json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}
			if corruption == "syntax" {
				data = append(data, '{')
			}
			if corruption == "bytes" {
				data = append(data, []byte(strings.Repeat(" ", maxManifestCacheBytes))...)
			}
			if err := os.WriteFile(cache, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if corruption == "mode" {
				if err := os.Chmod(cache, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c, notes, err := LoadEffective("", repo, env)
			if err != nil || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 0 {
				t.Fatalf("corrupt cache affected trusted hooks: %+v, %v, %v", c, notes, err)
			}
			info, err := os.Stat(cache)
			if err != nil || info.Size() > maxManifestCacheBytes || info.Mode().Perm() != 0o600 {
				t.Fatalf("cache not repaired to bounded private file: %v, %v", info, err)
			}
			data, err = os.ReadFile(cache)
			if err != nil {
				t.Fatal(err)
			}
			stored = manifestCacheData{}
			if err := json.Unmarshal(data, &stored); err != nil || len(stored.Files) > maxManifestCacheEntries {
				t.Fatalf("cache entry bound: %v, %v", stored, err)
			}
		})
	}
}
