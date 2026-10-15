package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	maxTrustBlobBytes      = 1 << 20
	maxTrustBlobStoreBytes = 64 << 20
)

func trustBlobsDir(env map[string]string) string {
	return filepath.Join(Dir(env), "trust", "blobs")
}

func prepareTrustBlobs(env map[string]string, store map[string]trustEntry) (int64, error) {
	for _, dir := range []string{Dir(env), filepath.Join(Dir(env), "trust"), trustBlobsDir(env)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return 0, err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return 0, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("trust blob directory must not be a symlink: %s", dir)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return 0, err
		}
	}
	keep := make(map[string]bool)
	for _, entry := range store {
		for _, file := range entry.Files {
			if !file.Missing && validDigest(file.SHA256) {
				keep[file.SHA256] = true
			}
		}
	}
	root := trustBlobsDir(env)
	var total int64
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if filepath.Dir(path) == root && keep[entry.Name()] && entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if err := os.Chmod(path, 0o600); err != nil {
				return err
			}
			total += info.Size()
			return nil
		}
		return os.Remove(path)
	})
	if err != nil {
		return 0, err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Remove(directories[i]); err != nil {
			return 0, err
		}
	}
	return total, nil
}

func storeTrustBlob(env map[string]string, hash string, data []byte, total *int64) (string, error) {
	if !validDigest(hash) || contentHash(data) != hash {
		return "", errors.New("trust blob hash does not match content")
	}
	if len(data) > maxTrustBlobBytes {
		return "no stored copy (over 1 MiB)", nil
	}
	path := filepath.Join(trustBlobsDir(env), hash)
	if _, err := os.Lstat(path); err == nil {
		_, err = readTrustBlob(env, hash)
		return "", err
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if *total > maxTrustBlobStoreBytes-int64(len(data)) {
		return "no stored copy (64 MiB store limit)", nil
	}
	file, err := os.CreateTemp(trustBlobsDir(env), ".blob-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// Another writer may have populated this digest while the temporary file was written.
	if _, err := os.Lstat(path); err == nil {
		_, err = readTrustBlob(env, hash)
		return "", err
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	*total += int64(len(data))
	return "", nil
}

func readTrustBlob(env map[string]string, hash string) ([]byte, error) {
	if !validDigest(hash) {
		return nil, errors.New("stored copy corrupt: invalid hash")
	}
	path := filepath.Join(trustBlobsDir(env), hash)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("no stored copy")
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTrustBlobBytes {
		return nil, errors.New("stored copy corrupt: not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxTrustBlobBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTrustBlobBytes || contentHash(data) != hash {
		return nil, errors.New("stored copy corrupt: content hash mismatch")
	}
	return data, nil
}
