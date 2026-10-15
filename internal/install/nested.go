package install

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// NewNestedToken creates an invocation-scoped helper bypass token. Call cleanup
// before exec or process exit; deferred cleanup cannot run after either.
func NewNestedToken(dir string) (token string, cleanup func(), err error) {
	if dir == "" {
		return "", nil, fmt.Errorf("nested token directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, fmt.Errorf("create nested token root: %w", err)
	}
	for _, path := range []string{filepath.Join(dir, "state"), filepath.Join(dir, "state", "nested")} {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return "", nil, fmt.Errorf("create nested token directory: %w", err)
		}
		if !privateNestedDir(path) {
			return "", nil, fmt.Errorf("nested token directory %q must be a private directory", path)
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", nil, fmt.Errorf("generate nested token: %w", err)
	}
	token = hex.EncodeToString(random[:])
	path := nestedTokenPath(dir, token)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("create nested token: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, fmt.Errorf("close nested token: %w", err)
	}
	var once sync.Once
	return token, func() { once.Do(func() { _ = os.Remove(path) }) }, nil
}

// ValidNestedToken recognizes a live invocation's helper bypass, not a security
// boundary: the invoking user can create or read these files.
func ValidNestedToken(dir string, env map[string]string) bool {
	token := env["GHOK_NESTED_TOKEN"]
	if dir == "" || env["GHOK_NESTED"] != "1" || len(token) != 64 {
		return false
	}
	for _, c := range token {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	if !privateNestedDir(filepath.Join(dir, "state")) || !privateNestedDir(filepath.Join(dir, "state", "nested")) {
		return false
	}
	info, err := os.Lstat(nestedTokenPath(dir, token))
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0
}

func privateNestedDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode().Perm()&0077 == 0
}

func nestedTokenPath(dir, token string) string {
	digest := sha256.Sum256([]byte(token))
	return filepath.Join(dir, "state", "nested", hex.EncodeToString(digest[:]))
}
