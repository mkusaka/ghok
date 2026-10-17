package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// LocalName is the personal, uncommitted project config (like CLAUDE.local.md).
const LocalName = ".ghok.local.yaml"

// localPath returns the repository-root .ghok.local.yaml when it exists.
func localPath(cwd, home string) (string, error) {
	root, err := projectRoot(cwd, home)
	if root == "" || err != nil {
		return "", err
	}
	p := filepath.Join(root, LocalName)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return p, nil
}

// LocalPath is where the local config for cwd lives (it may not exist yet).
func LocalPath(cwd string, env map[string]string) (string, error) {
	project, err := ProjectPath(cwd, env)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(project), LocalName), nil
}

// mergeLocal layers .ghok.local.yaml after the project config. It is written by
// the user, so its hooks run without trust; the same restrictions as a project
// config apply otherwise (no runtime/logging, policy can only tighten).
func mergeLocal(c *Config, notes []string, cwd string, env map[string]string) (*Config, []string, error) {
	ignore := func(path string, err error) (*Config, []string, error) {
		message := "local config ignored: " + err.Error()
		c.Diagnostics = append(c.Diagnostics, message)
		notes = append(notes, message)
		if env["GHOK_FAIL_CLOSED"] == "1" {
			return c, notes, err
		}
		return c, notes, nil
	}
	local, err := localPath(cwd, env["HOME"])
	if err != nil {
		return ignore(local, err)
	}
	if local == "" {
		return c, notes, nil
	}
	// A committed file would let any clone run hooks without trust.
	if gitTracked(filepath.Dir(local), LocalName) {
		notes = append(notes, fmt.Sprintf("Local config: %s (ignored: tracked by git; keep it uncommitted, e.g. in .git/info/exclude)", local))
		return c, notes, nil
	}
	warnWritable(local, &notes)
	data, _, err := readProjectData(local, c.Runtime.FileMaxBytes)
	if err != nil {
		return ignore(local, err)
	}
	p, n, err := parseProject(data, local)
	if err != nil {
		return ignore(local, err)
	}
	if field(n, "trust") != nil {
		return ignore(local, fmt.Errorf("%s: trust is only meaningful in .ghok.yaml", local))
	}
	for _, h := range p.Hooks {
		if h.Files != nil {
			return ignore(local, fmt.Errorf("%s: hook %s: files is only meaningful in .ghok.yaml (local hooks run without trust)", local, h.ID))
		}
	}
	notes = append(notes, projectUnknownKeys(n, local)...)
	merged := *c
	if err := mergeProject(&merged, p, n, local, cwd, env, &notes); err != nil {
		return ignore(local, err)
	}
	notes = append(notes, fmt.Sprintf("Local config: %s (loaded; hooks run without trust)", local))
	return &merged, notes, nil
}

// gitTracked reports whether git tracks name in dir. Without git (or outside a
// work tree) nothing is tracked.
func gitTracked(dir, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--error-unmatch", "--", name)
	return cmd.Run() == nil
}
