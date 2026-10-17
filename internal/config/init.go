package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Init scopes.
const (
	ScopeUser    = "user"
	ScopeProject = "project"
	ScopeLocal   = "local"
)

//go:embed templates/user.yaml
var userTemplate string

//go:embed templates/project.yaml
var projectTemplate string

//go:embed templates/local.yaml
var localTemplate string

// Init writes a commented starter config for scope and returns its path. It
// never overwrites an existing file.
func Init(scope, cwd string, env map[string]string) (string, error) {
	var path, body string
	switch scope {
	case ScopeUser:
		path, body = DefaultPath(env), userTemplate
	case ScopeProject:
		var err error
		if path, err = ProjectPath(cwd, env); err != nil {
			return "", err
		}
		body = projectTemplate
	case ScopeLocal:
		var err error
		if path, err = LocalPath(cwd, env); err != nil {
			return "", err
		}
		body = localTemplate
	default:
		return "", fmt.Errorf("unknown scope %q (want %s, %s or %s)", scope, ScopeUser, ScopeProject, ScopeLocal)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%s already exists; not overwriting", path)
	}
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}

// ExcludeLocal adds /.ghok.local.yaml to the repository's info/exclude (shared
// by all worktrees) unless an entry is already there; .gitignore is untouched.
func ExcludeLocal(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude").Output()
	if err != nil {
		return "", fmt.Errorf("locate info/exclude: %w", err)
	}
	path := strings.TrimSpace(string(out))
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	entry := "/" + LocalName
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return path, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + entry + "\n"); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}
