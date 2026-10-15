//go:build !darwin && !linux

package config

import "os"

// Without change time and file identity, preserved-mtime edits cannot safely hit.
func manifestCacheMetadata(info os.FileInfo) (manifestCacheEntry, bool) {
	return manifestCacheEntry{}, false
}
