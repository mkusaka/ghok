package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func blobTestEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"GHOK_CONFIG_DIR": filepath.Join(t.TempDir(), "config")}
}

func blobTestWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrustBlobsIntegrityAndWriteOnce(t *testing.T) {
	env := blobTestEnv(t)
	total, err := prepareTrustBlobs(env, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("secret=" + "unredacted\x00\r\n")
	hash := contentHash(data)
	if note, err := storeTrustBlob(env, hash, data, &total); err != nil || note != "" {
		t.Fatalf("store: note=%q err=%v", note, err)
	}
	got, err := readTrustBlob(env, hash)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read: data=%q err=%v", got, err)
	}
	path := filepath.Join(trustBlobsDir(env), hash)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != 0o600 {
		t.Fatalf("new blob permissions: %v", before.Mode())
	}
	if note, err := storeTrustBlob(env, hash, data, &total); err != nil || note != "" {
		t.Fatalf("dedup: note=%q err=%v", note, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || total != int64(len(data)) {
		t.Fatalf("dedup replaced file or double-counted bytes: total=%d", total)
	}
	if _, err := storeTrustBlob(env, hash, []byte("different"), &total); err == nil {
		t.Fatal("accepted mismatching content")
	}
	if _, err := storeTrustBlob(env, "../invalid", data, &total); err == nil {
		t.Fatal("accepted invalid digest")
	}
	blobTestWrite(t, path, []byte("corruption"))
	if _, err := readTrustBlob(env, hash); err == nil || !strings.Contains(err.Error(), "stored copy corrupt") {
		t.Fatalf("corruption not detected: %v", err)
	}
	if _, err := storeTrustBlob(env, hash, data, &total); err == nil {
		t.Fatal("accepted corrupt existing blob")
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != "corruption" {
		t.Fatalf("existing blob replaced: data=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(trustBlobsDir(env))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: entries=%v err=%v", entries, err)
	}
}

func TestTrustBlobBounds(t *testing.T) {
	env := blobTestEnv(t)
	total, err := prepareTrustBlobs(env, nil)
	if err != nil {
		t.Fatal(err)
	}
	large := bytes.Repeat([]byte("x"), maxTrustBlobBytes+1)
	if note, err := storeTrustBlob(env, contentHash(large), large, &total); err != nil || note != "no stored copy (over 1 MiB)" || total != 0 {
		t.Fatalf("oversized: note=%q total=%d err=%v", note, total, err)
	}
	boundary := large[:maxTrustBlobBytes]
	total = maxTrustBlobStoreBytes - maxTrustBlobBytes
	if note, err := storeTrustBlob(env, contentHash(boundary), boundary, &total); err != nil || note != "" || total != maxTrustBlobStoreBytes {
		t.Fatalf("exact limits: note=%q total=%d err=%v", note, total, err)
	}
	if note, err := storeTrustBlob(env, contentHash(boundary), boundary, &total); err != nil || note != "" || total != maxTrustBlobStoreBytes {
		t.Fatalf("dedup at limit: note=%q total=%d err=%v", note, total, err)
	}
	data := []byte("new")
	if note, err := storeTrustBlob(env, contentHash(data), data, &total); err != nil || note != "no stored copy (64 MiB store limit)" {
		t.Fatalf("store limit: note=%q err=%v", note, err)
	}
	if _, err := os.Lstat(filepath.Join(trustBlobsDir(env), contentHash(data))); !os.IsNotExist(err) {
		t.Fatalf("over-limit blob created: %v", err)
	}
}

func TestTrustBlobReadRejections(t *testing.T) {
	env := blobTestEnv(t)
	if _, err := prepareTrustBlobs(env, nil); err != nil {
		t.Fatal(err)
	}
	hash := contentHash([]byte("expected"))
	if _, err := readTrustBlob(env, hash); err == nil || !strings.Contains(err.Error(), "no stored copy") {
		t.Fatalf("missing: %v", err)
	}
	for _, hash := range []string{"", "../outside", strings.Repeat("A", 64), strings.Repeat("0", 63)} {
		if _, err := readTrustBlob(env, hash); err == nil {
			t.Fatalf("accepted invalid digest %q", hash)
		}
	}
	path := filepath.Join(trustBlobsDir(env), hash)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrustBlob(env, hash); err == nil || !strings.Contains(err.Error(), "stored copy corrupt") {
		t.Fatalf("directory: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	blobTestWrite(t, outside, []byte("expected"))
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrustBlob(env, hash); err == nil || !strings.Contains(err.Error(), "stored copy corrupt") {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTrustBlobBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrustBlob(env, hash); err == nil || !strings.Contains(err.Error(), "stored copy corrupt") {
		t.Fatalf("oversized read: %v", err)
	}
}

func TestPrepareTrustBlobsGCAndPermissions(t *testing.T) {
	env := blobTestEnv(t)
	root := trustBlobsDir(env)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{Dir(env), filepath.Dir(root), root} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	kept := []byte("shared content")
	shared := contentHash(kept)
	orphan := contentHash([]byte("orphan"))
	missing := contentHash([]byte("missing"))
	for _, name := range []string{shared, orphan, missing, ".blob-abandoned"} {
		blobTestWrite(t, filepath.Join(root, name), kept)
	}
	if err := os.Chmod(filepath.Join(root, shared), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	blobTestWrite(t, filepath.Join(nested, shared), kept)
	outside := filepath.Join(t.TempDir(), "outside")
	blobTestWrite(t, outside, []byte("untouched"))
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	store := map[string]trustEntry{
		"first":  {Files: []trustedFile{{SHA256: shared}, {SHA256: missing, Missing: true}}},
		"second": {Files: []trustedFile{{SHA256: shared}}},
	}
	total, err := prepareTrustBlobs(env, store)
	if err != nil || total != int64(len(kept)) {
		t.Fatalf("prepare: total=%d err=%v", total, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != shared {
		t.Fatalf("GC: entries=%v err=%v", entries, err)
	}
	for _, dir := range []string{Dir(env), filepath.Dir(root), root} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory permissions: %s: %v, %v", dir, info, err)
		}
	}
	info, err := os.Stat(filepath.Join(root, shared))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("blob permissions: %v, %v", info, err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("followed symlink during GC: data=%q err=%v", got, err)
	}
	delete(store, "first")
	if total, err := prepareTrustBlobs(env, store); err != nil || total != int64(len(kept)) {
		t.Fatalf("shared reference removed: total=%d err=%v", total, err)
	}
	if total, err := prepareTrustBlobs(env, nil); err != nil || total != 0 {
		t.Fatalf("empty-store GC: total=%d err=%v", total, err)
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty-store leftovers: entries=%v err=%v", entries, err)
	}
}

func TestPrepareTrustBlobsRejectsSymlinkDirectories(t *testing.T) {
	for _, component := range []string{"config", "trust", "blobs"} {
		t.Run(component, func(t *testing.T) {
			env := blobTestEnv(t)
			var path string
			switch component {
			case "config":
				path = Dir(env)
			case "trust":
				path = filepath.Join(Dir(env), "trust")
			case "blobs":
				path = trustBlobsDir(env)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if err := os.Chmod(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := prepareTrustBlobs(env, nil); err == nil {
				t.Fatal("accepted a symlink directory")
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("changed symlink target: %v, %v", info, err)
			}
		})
	}
}
