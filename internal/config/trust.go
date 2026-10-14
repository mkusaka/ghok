package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// IgnoredHook is reporting metadata, never an executable hook selection.
type IgnoredHook struct {
	Hook
	Reason string `json:"reason"`
}

type trustEntry struct {
	SHA256    string    `json:"sha256"`
	TrustedAt time.Time `json:"trustedAt"`
}

func projectDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func trustStorePath(env map[string]string) string {
	return filepath.Join(Dir(env), "trust.json")
}

func parseTrustStore(data []byte) (map[string]trustEntry, error) {
	var store map[string]trustEntry
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("trust store must be a JSON object")
	}
	for path, entry := range store {
		digest, err := hex.DecodeString(entry.SHA256)
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || err != nil || len(digest) != sha256.Size || entry.TrustedAt.IsZero() {
			return nil, fmt.Errorf("invalid trust entry for %q", path)
		}
	}
	return store, nil
}

// A malformed or unreadable store cannot grant even partially decoded trust.
func readTrustStore(env map[string]string) (map[string]trustEntry, []string) {
	path := trustStorePath(env)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]trustEntry), nil
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("trust store must be a regular file, not a symlink")
	}
	var store map[string]trustEntry
	if err == nil {
		var data []byte
		data, err = os.ReadFile(path)
		if err == nil {
			store, err = parseTrustStore(data)
		}
	}
	if err != nil {
		return make(map[string]trustEntry), []string{fmt.Sprintf("Trust store: %s is invalid or unreadable (%v); no project hooks trusted; run ghok config trust", path, err)}
	}
	return store, nil
}

func writeTrustStore(env map[string]string, store map[string]trustEntry) error {
	path := trustStorePath(env)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".trust-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	// CreateTemp uses 0600; explicit chmod also handles restrictive umasks.
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// trustProjectPath retains the repository directory for relative hook commands
// while separately returning the canonical config identity used by the store.
func trustProjectPath(path, cwd string, env map[string]string) (project, canonical string, err error) {
	if path == "" {
		path, err = ProjectPath(cwd, env)
		if err != nil {
			return "", "", err
		}
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("project config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("project config %s must be a regular file", path)
	}
	project, err = ProjectPath(filepath.Dir(path), env)
	if err != nil {
		return "", "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	if filepath.Join(dir, filepath.Base(path)) != project {
		return "", "", fmt.Errorf("%s is not the repository-root .ghok.yaml (%s)", path, project)
	}
	canonical, err = filepath.EvalSymlinks(project)
	return project, canonical, err
}

// TrustProject authorizes the exact bytes inspected here. It never runs hooks
// and validates the effective configuration before granting hook execution.
func TrustProject(path, cwd string, env map[string]string) (string, []Hook, []string, error) {
	project, canonical, err := trustProjectPath(path, cwd, env)
	if err != nil {
		return "", nil, nil, err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return "", nil, nil, err
	}
	p, n, err := parseProject(data, project)
	if err != nil {
		return "", nil, nil, err
	}
	var notes []string
	c, err := load(DefaultPath(env), &notes)
	if err != nil {
		return "", nil, notes, err
	}
	if err := mergeProject(c, p, n, project, filepath.Dir(project), env, "", &notes); err != nil {
		return "", nil, notes, err
	}
	store, storeNotes := readTrustStore(env)
	notes = append(notes, storeNotes...)
	store[canonical] = trustEntry{SHA256: projectDigest(data), TrustedAt: time.Now().UTC()}
	if err := writeTrustStore(env, store); err != nil {
		return "", nil, notes, err
	}
	active := p.Hooks[:0]
	for _, h := range p.Hooks {
		if h.Enabled == nil || *h.Enabled {
			active = append(active, h)
		}
	}
	return canonical, active, notes, nil
}

// UntrustProject removes hook authorization without changing declarative policy.
// A missing trust entry is success; a missing project file is an error.
func UntrustProject(path, cwd string, env map[string]string) (string, []string, error) {
	_, canonical, err := trustProjectPath(path, cwd, env)
	if err != nil {
		return "", nil, err
	}
	store, notes := readTrustStore(env)
	if _, exists := store[canonical]; !exists {
		return canonical, notes, nil
	}
	delete(store, canonical)
	if err := writeTrustStore(env, store); err != nil {
		return "", notes, err
	}
	return canonical, notes, nil
}
