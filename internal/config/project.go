package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// LoadEffective never executes hooks. Notes are for doctor/explain, not normal
// wrapper stderr. Restricted project sections are rejected, even when trusted.
func LoadEffective(global, cwd string, env map[string]string) (*Config, []string, error) {
	if global == "" {
		global = DefaultPath(env)
	}
	absolute, err := filepath.Abs(global)
	if err != nil {
		return nil, nil, err
	}
	global = absolute
	c, err := Load(global)
	if err != nil {
		return nil, nil, err
	}
	var notes []string
	warnWritable(global, &notes)
	for _, h := range c.Hooks {
		warnWritable(hookPermissionPath(c.HookCommandPath(h), cwd, env), &notes)
	}
	project, err := findProject(cwd, env["HOME"])
	if err != nil {
		return nil, notes, err
	}
	if project == "" {
		return c, notes, nil
	}
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, notes, err
	}
	trusted := false
	for _, p := range c.Trust.Projects {
		if filepath.IsAbs(p) && filepath.Clean(p) == resolved {
			trusted = true
			break
		}
	}
	if !trusted {
		notes = append(notes, fmt.Sprintf("found untrusted project config at %s; ignored", resolved))
		return c, notes, nil
	}
	warnWritable(project, &notes)
	data, err := os.ReadFile(project)
	if err != nil {
		return nil, notes, err
	}
	n, err := document(data)
	if err != nil {
		return nil, notes, fmt.Errorf("project config %s: %w", project, err)
	}
	var restricted []error
	for _, key := range []string{"runtime", "logging", "trust"} {
		if v := field(n, key); v != nil {
			restricted = append(restricted, fmt.Errorf("line %d: project %s: cannot set %s", v.Line, project, key))
		}
	}
	if err := errors.Join(restricted...); err != nil {
		return nil, notes, err
	}
	// Resolve project hooks against the project config's containing directory,
	// not the global config or the destination of a symlinked config file.
	p, err := Parse(data, filepath.Dir(project))
	if err != nil {
		return nil, notes, fmt.Errorf("project config %s: %w", project, err)
	}
	for _, h := range p.Hooks {
		warnWritable(hookPermissionPath(p.HookCommandPath(h), cwd, env), &notes)
		if len(h.Command) > 0 {
			command := append([]string(nil), h.Command...)
			command[0] = p.HookCommandPath(h)
			h.Command = command
		}
		c.Hooks = append(c.Hooks, h)
	}
	c.Policy.Rules = append(c.Policy.Rules, p.Policy.Rules...)
	c.Policy.ProtectedRepositories = append(c.Policy.ProtectedRepositories, p.Policy.ProtectedRepositories...)
	policy := field(n, "policy")
	if field(policy, "mode") != nil {
		if c.Policy.Mode == ModeEnforce && p.Policy.Mode == ModeObserve {
			notes = append(notes, "project policy.mode would weaken global policy; ignored")
		} else {
			c.Policy.Mode = p.Policy.Mode
		}
	}
	rank := map[string]int{UnknownAllow: 0, UnknownAudit: 1, UnknownDeny: 2}
	for _, pair := range []struct {
		key   string
		dst   *string
		value string
	}{
		{"unknownMutation", &c.Policy.UnknownMutation, p.Policy.UnknownMutation},
		{"protectedUnknown", &c.Policy.ProtectedUnknown, p.Policy.ProtectedUnknown},
	} {
		if field(policy, pair.key) == nil {
			continue
		}
		if rank[pair.value] < rank[*pair.dst] {
			notes = append(notes, "project policy."+pair.key+" would weaken global policy; ignored")
		} else {
			*pair.dst = pair.value
		}
	}
	if err := c.Validate(); err != nil {
		return nil, notes, fmt.Errorf("effective config: %w", err)
	}
	return c, notes, nil
}

func findProject(cwd, home string) (string, error) {
	root, err := projectRoot(cwd, home)
	if root == "" || err != nil {
		return "", err
	}
	p := filepath.Join(root, ".ghok.yaml")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return "", nil
}

// ProjectPath is where the project config for cwd lives: .ghok.yaml at the
// nearest enclosing directory containing .git (searching up to $HOME).
func ProjectPath(cwd string, env map[string]string) (string, error) {
	root, err := projectRoot(cwd, env["HOME"])
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("%s is not inside a git repository; project config lives at the repository root", cwd)
	}
	return filepath.Join(root, ".ghok.yaml"), nil
}

// projectRoot returns the nearest directory (symlinks resolved) at or above
// cwd that contains .git, stopping at home or the filesystem root ("" if none).
func projectRoot(cwd, home string) (string, error) {
	d, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	d, err = filepath.EvalSymlinks(d)
	if err != nil {
		return "", err
	}
	if home != "" {
		home, err = filepath.Abs(home)
		if err != nil {
			return "", err
		}
		if h, e := filepath.EvalSymlinks(home); e == nil {
			home = h
		}
	}
	for {
		_, err := os.Stat(filepath.Join(d, ".git"))
		if err == nil {
			return d, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(d)
		if d == home || d == parent {
			return "", nil
		}
		d = parent
	}
}

func warnWritable(p string, notes *[]string) {
	// Bare executable names use PATH at execution time; no config-relative file
	// exists to inspect. Missing hook files are the runner/doctor's concern.
	if !filepath.IsAbs(p) && filepath.Base(p) == p {
		return
	}
	if info, err := os.Stat(p); err == nil && info.Mode().Perm()&0022 != 0 {
		*notes = append(*notes, fmt.Sprintf("%s is group/world-writable", p))
	}
}

// fieldThroughMerge implements YAML merge lookup without following cyclic
// aliases indefinitely. Explicit keys take precedence over merged keys.
func fieldThroughMerge(n *yaml.Node, key string, seen map[*yaml.Node]bool) *yaml.Node {
	if n == nil || seen[n] {
		return nil
	}
	seen[n] = true
	if n.Kind == yaml.AliasNode {
		return fieldThroughMerge(n.Alias, key, seen)
	}
	if n.Kind == yaml.SequenceNode {
		for _, child := range n.Content {
			if v := fieldThroughMerge(child, key, seen); v != nil {
				return v
			}
		}
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Tag == "!!merge" {
			if v := fieldThroughMerge(n.Content[i+1], key, seen); v != nil {
				return v
			}
		}
	}
	return nil
}

func hookPermissionPath(command, cwd string, env map[string]string) string {
	if command == "" || filepath.IsAbs(command) {
		return command
	}
	for _, dir := range filepath.SplitList(env["PATH"]) {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, command)
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
			return candidate
		}
	}
	return ""
}
