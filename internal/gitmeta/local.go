// Package gitmeta reads local Git metadata without executing git.
package gitmeta

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxBytes = 1 << 20

func ReadBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err == nil && len(data) > maxBytes {
		return nil, fmt.Errorf("git metadata exceeds 1 MiB")
	}
	return data, err
}

func Absolute(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

func Directory(cwd string, env map[string]string) (string, error) {
	if dir := env["GIT_DIR"]; dir != "" {
		return Absolute(cwd, dir), nil
	}
	if work := env["GIT_WORK_TREE"]; work != "" {
		cwd = Absolute(cwd, work)
	}
	ceilings := make(map[string]bool)
	for _, dir := range filepath.SplitList(env["GIT_CEILING_DIRECTORIES"]) {
		if filepath.IsAbs(dir) {
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				dir = real
			}
			ceilings[filepath.Clean(dir)] = true
		}
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		// Git does not search the ceiling itself; starting inside it is allowed.
		if dir != cwd && ceilings[dir] {
			break
		}
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return candidate, nil
			}
			data, err := ReadBounded(candidate)
			if err != nil {
				return "", err
			}
			text := strings.TrimSpace(string(data))
			if !strings.HasPrefix(text, "gitdir: ") || strings.TrimSpace(text[8:]) == "" {
				return "", fmt.Errorf("malformed .git file")
			}
			return Absolute(dir, strings.TrimSpace(text[8:])), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", fmt.Errorf("no git repository found")
}

func ValidSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func ReadRef(gitdir, common, ref string, depth int) (string, error) {
	if depth > 10 {
		return "", fmt.Errorf("git symbolic ref depth exceeded")
	}
	if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "\\") || path.Clean(ref) != ref || strings.Contains(ref, "..") {
		return "", fmt.Errorf("invalid git ref")
	}
	for _, dir := range []string{gitdir, common} {
		data, err := ReadBounded(filepath.Join(dir, filepath.FromSlash(ref)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(data))
		if strings.HasPrefix(value, "ref: ") {
			return ReadRef(gitdir, common, strings.TrimSpace(value[5:]), depth+1)
		}
		if !ValidSHA(value) {
			return "", fmt.Errorf("invalid git ref SHA")
		}
		return value, nil
	}
	data, err := ReadBounded(filepath.Join(common, "packed-refs"))
	if os.IsNotExist(err) {
		return "", fmt.Errorf("branch has no commit (unborn or missing ref)")
	}
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			if !ValidSHA(fields[0]) {
				return "", fmt.Errorf("invalid packed ref SHA")
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("branch has no commit (unborn or missing ref)")
}
