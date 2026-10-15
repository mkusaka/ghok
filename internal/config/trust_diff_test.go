package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustDiffUsesTrustedWorkingTreeBytes(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	hook := filepath.Join(repo, "hook")
	writeFixture(t, hook, "dirty trusted code\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, hook, "edited after trust\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		_, _, notes, diff, err := TrustProject("", repo, env, dryRun, false)
		if err != nil || !bytes.Contains(diff, []byte("-dirty trusted code\n+edited after trust\n")) || bytes.Contains(diff, []byte("-reviewed code")) {
			t.Fatalf("diff must use the dirty trusted baseline: %s, %v", diff, err)
		}
		if !strings.Contains(strings.Join(notes, "\n"), "hook: mode 0644 → 0755") {
			t.Fatalf("executable-bit change omitted: %v", notes)
		}
	}
	writeFixture(t, hook, "third version\n")
	_, _, notes, diff, err := TrustProject("", repo, env, true, true)
	if err != nil || len(diff) != 0 || !strings.Contains(strings.Join(notes, "\n"), "Changed files since trust: hook") {
		t.Fatalf("no-diff must retain the review summary: %v, %q, %v", notes, diff, err)
	}
	_, _, _, diff, err = TrustProject("", repo, env, true, false)
	if err != nil || !bytes.Contains(diff, []byte("-edited after trust\n+third version\n")) {
		t.Fatalf("re-trust failed to refresh the exact-byte baseline: %s, %v", diff, err)
	}
}

func TestTrustDiffBinaryAndLineBounds(t *testing.T) {
	for _, tt := range []struct {
		name, old, next, want string
	}{
		{"binary", "old\x00bytes", "new\x00bytes", "binary files differ (old "},
		{"NUL outside prefix", strings.Repeat("x", 8192) + "\x00old", strings.Repeat("x", 8192) + "\x00new", "--- trusted/hook\n"},
		{"line boundary", strings.Repeat("same\n", 19999) + "old\n", strings.Repeat("same\n", 19999) + "new\n", "-old\n+new\n"},
		{"too many old lines", strings.Repeat("old\n", 20001), "new\n", "diff too large; files changed"},
		{"too many new lines", "old\n", strings.Repeat("new\n", 20001), "diff too large; files changed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			writeFixture(t, filepath.Join(repo, "hook"), tt.old)
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(repo, "hook"), tt.next)
			_, _, _, diff, err := TrustProject("", repo, env, true, false)
			if err != nil || !bytes.Contains(diff, []byte(tt.want)) {
				t.Fatalf("review omitted %q: %.300s, %v", tt.want, diff, err)
			}
			if tt.name == "binary" {
				oldHash, newHash := contentHash([]byte(tt.old)), contentHash([]byte(tt.next))
				want := "binary files differ (old " + oldHash[:8] + ", new " + newHash[:8] + ")"
				if !bytes.Contains(diff, []byte(want)) || bytes.Contains(diff, []byte("--- ")) {
					t.Fatalf("binary diff must show hashes, not contents: %s", diff)
				}
			}
		})
	}
}

func TestTrustDiffUnavailableCopies(t *testing.T) {
	for _, kind := range []string{"large", "corrupt", "missing"} {
		t.Run(kind, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			old := "reviewed code\n"
			if kind == "large" {
				old = strings.Repeat("x", maxTrustBlobBytes+1)
				writeFixture(t, filepath.Join(repo, "hook"), old)
			}
			_, _, notes, _, err := TrustProject("", repo, env, false, false)
			if err != nil {
				t.Fatal(err)
			}
			blob := filepath.Join(trustBlobsDir(env), contentHash([]byte(old)))
			switch kind {
			case "large":
				if !strings.Contains(strings.Join(notes, "\n"), "hook: no stored copy (over 1 MiB)") {
					t.Fatalf("hash-only trust omitted limit note: %v", notes)
				}
				if _, err := os.Stat(blob); !os.IsNotExist(err) {
					t.Fatalf("large file should remain hash-only: %v", err)
				}
			case "corrupt":
				writeFixture(t, blob, "corrupted copy")
			case "missing":
				if err := os.Remove(blob); err != nil {
					t.Fatal(err)
				}
			}
			writeFixture(t, filepath.Join(repo, "hook"), "new code\n")
			_, _, _, diff, err := TrustProject("", repo, env, true, false)
			want := "no stored copy"
			if kind == "corrupt" {
				want = "stored copy corrupt"
			}
			if err != nil || !bytes.Contains(diff, []byte(want)) || bytes.Contains(diff, []byte("--- ")) {
				t.Fatalf("unavailable baseline must not generate a misleading diff: %s, %v", diff, err)
			}
		})
	}
}

func TestTrustBlobGCLifecycle(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	oldHash := contentHash([]byte("reviewed code\n"))
	writeFixture(t, filepath.Join(repo, "hook"), "new trusted code\n")
	orphan := filepath.Join(trustBlobsDir(env), contentHash([]byte("orphan")))
	writeFixture(t, orphan, "orphan")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("trust did not collect unreferenced blob: %v", err)
	}
	if _, _, err := UntrustProject("", repo, env); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(trustBlobsDir(env), contentHash([]byte("new trusted code\n")))); !os.IsNotExist(err) {
		t.Fatalf("untrust retained unreferenced contents: %v", err)
	}
	if data, err := readTrustBlob(env, oldHash); err != nil || string(data) != "reviewed code\n" {
		t.Fatalf("untrust removed a retained version's baseline: %q, %v", data, err)
	}
	if _, err := UntrustAllProjects(env); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(trustBlobsDir(env))
	if err != nil || len(entries) != 0 {
		t.Fatalf("untrust --all did not purge stored contents: %v, %v", entries, err)
	}
}

func TestTrustRemoteNormalization(t *testing.T) {
	for _, raw := range []string{"git@EXAMPLE.com:Owner/Repo.git", "ssh://git@EXAMPLE.com:22/Owner/Repo.git/", "https://user:" + "private@EXAMPLE.com:443/Owner/Repo/"} {
		if got := normalizeTrustRemote(raw, "/repo"); got != "example.com/Owner/Repo" {
			t.Fatalf("transport or credential normalization: %q -> %q", raw, got)
		}
	}
}
