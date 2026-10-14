package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Init scopes.
const (
	ScopeUser    = "user"
	ScopeProject = "project"
)

//go:embed templates/user.yaml
var userTemplate string

//go:embed templates/project.yaml
var projectTemplate string

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
	default:
		return "", fmt.Errorf("unknown scope %q (want %s or %s)", scope, ScopeUser, ScopeProject)
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
