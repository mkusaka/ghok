package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

// projectForbidden are the top-level sections a project .ghok.yaml may not set.
var projectForbidden = []string{"runtime", "logging"}

// LoadEffective never executes hooks. Valid project policy always applies; only
// content-trusted project hooks are active. Project failures retain user config.
func LoadEffective(global, cwd string, env map[string]string) (*Config, []string, error) {
	if global == "" {
		global = DefaultPath(env)
	}
	absolute, err := filepath.Abs(global)
	if err != nil {
		return nil, nil, err
	}
	global = absolute
	var notes []string
	c, err := load(global, &notes)
	if err != nil {
		return nil, notes, err
	}
	warnWritable(global, &notes)
	for _, h := range c.Hooks {
		warnWritable(hookPermissionPath(c.HookCommandPath(h), cwd, env), &notes)
	}
	sourceIndex := -1
	var project string
	ignoreProject := func(err error) (*Config, []string, error) {
		message := "project config ignored: " + err.Error()
		c.Diagnostics = append(c.Diagnostics, message)
		if sourceIndex >= 0 {
			notes[sourceIndex] = fmt.Sprintf("Project config: %s (ignored; user config retained)", project)
		}
		notes = append(notes, message)
		if env["GHOK_FAIL_CLOSED"] == "1" {
			return c, notes, err
		}
		return c, notes, nil
	}
	project, err = findProject(cwd, env["HOME"])
	if err != nil {
		return ignoreProject(err)
	}
	if project == "" {
		return c, notes, nil
	}
	sourceIndex = len(notes)
	notes = append(notes, fmt.Sprintf("Project config: %s (loading declarative policy)", project))
	warnWritable(project, &notes)
	data, canonical, err := readProjectData(project, c.Runtime.FileMaxBytes)
	if err != nil {
		return ignoreProject(err)
	}
	p, n, err := parseProject(data, project)
	if err != nil {
		return ignoreProject(err)
	}
	notes = append(notes, projectUnknownKeys(n, project)...)
	reason := ""
	var ignored *IgnoredProject
	if len(p.Hooks) != 0 {
		digest, files, err := projectManifest(data, p, filepath.Dir(project), c.Runtime.FileMaxBytes, env, false)
		if err != nil {
			return ignoreProject(fmt.Errorf("project config %s: %w", project, err))
		}
		store, trustNotes := readTrustStore(env)
		notes = append(notes, trustNotes...)
		if _, ok := store[digest]; !ok {
			reason = "untrusted"
			ignored = &IgnoredProject{Path: canonical, Digest: digest}
			current := currentTrustEntry(project, canonical, files, env)
			comparison, latest := trustComparisonNotes(current, store)
			notes = append(notes, comparison...)
			if latest != nil {
				reason = "changed since trust"
				ignored.ChangedFiles = changedTrustFiles(files, latest.Files)
			}
		}
	}
	status := "policy loaded; hooks trusted"
	if len(p.Hooks) == 0 {
		status = "policy loaded; no project hooks"
	}
	if reason != "" {
		status = "policy loaded; hooks ignored (" + reason + "); run ghok config trust"
	}
	notes[sourceIndex] = fmt.Sprintf("Project config: %s (%s)", project, status)
	merged := *c
	if err := mergeProject(&merged, p, n, project, cwd, env, reason, &notes); err != nil {
		return ignoreProject(err)
	}
	merged.IgnoredProject = ignored
	return &merged, notes, nil
}

func parseProject(data []byte, project string) (*Config, *yaml.Node, error) {
	n, err := document(data)
	if err != nil {
		return nil, nil, fmt.Errorf("project config %s: %w", project, err)
	}
	var restricted []error
	for _, key := range projectForbidden {
		if v := field(n, key); v != nil {
			restricted = append(restricted, fmt.Errorf("line %d: project %s: cannot set %s", v.Line, project, key))
		}
	}
	if err := errors.Join(restricted...); err != nil {
		return nil, nil, err
	}
	// Resolve hooks against the repository config's directory, not the global
	// config or the destination of a symlinked project file.
	p, err := parse(data, filepath.Dir(project), false)
	if err != nil {
		return nil, nil, fmt.Errorf("project config %s: %w", project, err)
	}
	for _, h := range p.Hooks {
		command := h.Command[0]
		if !filepath.IsAbs(command) && !strings.HasPrefix(command, "./") {
			return nil, nil, fmt.Errorf("project config %s: hook %s command must use ./relative or an absolute path; bare PATH names are not allowed", project, h.ID)
		}
	}
	for _, path := range p.Trust.Include {
		if !filepath.IsLocal(path) {
			return nil, nil, fmt.Errorf("project config %s: trust.include %q must be repository-relative and cannot escape repository root", project, path)
		}
	}
	return p, n, nil
}

// Unknown project fields are advisory, including fields reached through aliases
// and YAML merges. Dynamic maps (hook/match env) accept arbitrary keys.
func projectUnknownKeys(n *yaml.Node, project string) []string {
	var notes []string
	seen := make(map[*yaml.Node]bool)
	var visit func(*yaml.Node, reflect.Type, string)
	visit = func(n *yaml.Node, t reflect.Type, path string) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		switch n.Kind {
		case yaml.AliasNode:
			visit(n.Alias, t, path)
		case yaml.SequenceNode:
			for i, child := range n.Content {
				if t.Kind() == reflect.Slice {
					visit(child, t.Elem(), fmt.Sprintf("%s[%d]", path, i))
				} else {
					visit(child, t, path) // YAML merge sequence.
				}
			}
		case yaml.MappingNode:
			if t.Kind() != reflect.Struct {
				return
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value := n.Content[i], n.Content[i+1]
				if key.Tag == "!!merge" {
					visit(value, t, path)
					continue
				}
				name := key.Value
				if path != "" {
					name = path + "." + name
				}
				found := false
				for j := range t.NumField() {
					f := t.Field(j)
					tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
					if tag != "-" && tag == key.Value {
						visit(value, f.Type, name)
						found = true
						break
					}
				}
				if !found {
					notes = append(notes, fmt.Sprintf("project config %s: line %d: unknown key %s ignored", project, key.Line, name))
				}
			}
		}
	}
	visit(n, reflect.TypeFor[Config](), "")
	return notes
}

func mergeProject(c, p *Config, n *yaml.Node, project, cwd string, env map[string]string, reason string, notes *[]string) error {
	for i := range p.Hooks {
		h := &p.Hooks[i]
		h.Source = project
		h.Command[0] = p.HookCommandPath(*h)
		warnWritable(hookPermissionPath(h.Command[0], cwd, env), notes)
		if reason != "" {
			c.IgnoredHooks = append(c.IgnoredHooks, IgnoredHook{Hook: *h, Reason: reason})
		} else {
			c.Hooks = append(c.Hooks, *h)
		}
	}
	c.Policy.Rules = append(c.Policy.Rules, p.Policy.Rules...)
	c.Policy.ProtectedRepositories = append(c.Policy.ProtectedRepositories, p.Policy.ProtectedRepositories...)
	policy := field(n, "policy")
	if field(policy, "mode") != nil {
		if c.Policy.Mode == ModeEnforce && p.Policy.Mode == ModeObserve {
			*notes = append(*notes, "project policy.mode would weaken global policy; ignored")
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
		{"unknownInput", &c.Policy.UnknownInput, p.Policy.UnknownInput},
		{"protectedUnknown", &c.Policy.ProtectedUnknown, p.Policy.ProtectedUnknown},
	} {
		if field(policy, pair.key) == nil {
			continue
		}
		if rank[pair.value] < rank[*pair.dst] {
			*notes = append(*notes, "project policy."+pair.key+" would weaken global policy; ignored")
		} else {
			*pair.dst = pair.value
		}
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("effective config: %w", err)
	}
	return nil
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
// nearest enclosing directory containing .git (searching strictly below $HOME).
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
// cwd that contains .git, stopping before home or at the filesystem root.
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
		if d == home {
			return "", nil
		}
		_, err := os.Stat(filepath.Join(d, ".git"))
		if err == nil {
			return d, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(d)
		if d == parent {
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
