package config

import (
	"os"
	"syscall"
)

func manifestCacheMetadata(info os.FileInfo) (manifestCacheEntry, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return manifestCacheEntry{}, false
	}
	return manifestCacheEntry{
		Size: info.Size(), Modified: info.ModTime().UnixNano(), Mode: uint32(info.Mode()),
		Device: uint64(stat.Dev), Inode: stat.Ino,
		ChangedSec: int64(stat.Ctim.Sec), ChangedNS: int64(stat.Ctim.Nsec),
	}, true
}
