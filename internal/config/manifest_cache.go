package config

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	maxManifestCacheEntries = 4096
	maxManifestCacheBytes   = 8 << 20
)

// Only hashes are cached: the manifest still resolves and stats every path,
// enforces limits, and reads the project YAML on every invocation. Entries span
// projects; overflow evicts the oldest computed hashes, not other whole manifests.
type manifestCache struct {
	path    string
	entries map[string]manifestCacheEntry
	dirty   bool
}

type manifestCacheEntry struct {
	Size       int64  `json:"size"`
	Modified   int64  `json:"modified"`
	ChangedSec int64  `json:"changedSec"`
	ChangedNS  int64  `json:"changedNS"`
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
	Mode       uint32 `json:"mode"`
	SHA256     string `json:"sha256"`
	Updated    int64  `json:"updated"`
}

type manifestCacheData struct {
	Version int                           `json:"version"`
	Files   map[string]manifestCacheEntry `json:"files"`
}

func readManifestCache(env map[string]string) *manifestCache {
	cache := &manifestCache{path: filepath.Join(Dir(env), "state", "manifest-cache.json"), entries: make(map[string]manifestCacheEntry)}
	info, err := os.Lstat(cache.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxManifestCacheBytes {
		return cache
	}
	file, err := os.Open(cache.path)
	if err != nil {
		return cache
	}
	data, err := io.ReadAll(io.LimitReader(file, maxManifestCacheBytes+1))
	_ = file.Close()
	if err != nil || len(data) > maxManifestCacheBytes {
		return cache
	}
	var stored manifestCacheData
	if json.Unmarshal(data, &stored) != nil || stored.Version != 1 || len(stored.Files) > maxManifestCacheEntries {
		return cache
	}
	for path, entry := range stored.Files {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || entry.Size < 0 || !validDigest(entry.SHA256) {
			return cache
		}
	}
	if stored.Files != nil {
		cache.entries = stored.Files
	}
	return cache
}

func (cache *manifestCache) lookup(path string, info os.FileInfo) (string, bool) {
	metadata, ok := manifestCacheMetadata(info)
	if !ok {
		return "", false
	}
	entry, exists := cache.entries[path]
	metadata.SHA256, metadata.Updated = entry.SHA256, entry.Updated
	if !exists || metadata != entry {
		return "", false
	}
	return entry.SHA256, true
}

func (cache *manifestCache) remember(path string, before, after os.FileInfo, hash string) {
	metadata, ok := manifestCacheMetadata(before)
	current, currentOK := manifestCacheMetadata(after)
	if !ok || !currentOK || metadata != current {
		return
	}
	metadata.SHA256 = hash
	metadata.Updated = time.Now().UnixNano()
	cache.entries[path] = metadata
	cache.dirty = true
}

// Cache persistence is best-effort, never a condition of granting or denying
// trust. Rename publishes a complete private snapshot and leaves no temp files.
func (cache *manifestCache) save() {
	if !cache.dirty {
		return
	}
	if len(cache.entries) > maxManifestCacheEntries {
		paths := make([]string, 0, len(cache.entries))
		for path := range cache.entries {
			paths = append(paths, path)
		}
		sort.Slice(paths, func(i, j int) bool {
			left, right := cache.entries[paths[i]].Updated, cache.entries[paths[j]].Updated
			if left == right {
				return paths[i] < paths[j]
			}
			return left < right
		})
		for _, path := range paths[:len(paths)-maxManifestCacheEntries] {
			delete(cache.entries, path)
		}
	}
	data, err := json.Marshal(manifestCacheData{Version: 1, Files: cache.entries})
	if err != nil || len(data) > maxManifestCacheBytes {
		return
	}
	dir := filepath.Dir(cache.path)
	if os.MkdirAll(dir, 0o700) != nil || os.Chmod(dir, 0o700) != nil {
		return
	}
	file, err := os.CreateTemp(dir, ".manifest-cache-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if file.Chmod(0o600) != nil {
		_ = file.Close()
		return
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return
	}
	if file.Close() != nil {
		return
	}
	_ = os.Rename(file.Name(), cache.path)
}
