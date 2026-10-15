package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	withheld := make(map[string]IgnoredHook)
	var ignored *IgnoredProject
	if len(p.Hooks) != 0 {
		files, err := projectManifest(p, filepath.Dir(project), c.Runtime.FileMaxBytes, env, false)
		if err != nil {
			return ignoreProject(fmt.Errorf("project config %s: %w", project, err))
		}
		store, trustNotes := readTrustStore(env)
		notes = append(notes, trustNotes...)
		current := currentTrustEntry(project, canonical, p, files, env)
		previous := store[contentHash([]byte(canonical))]
		active := 0
		for _, h := range p.Hooks {
			digest := current.HookDigests[h.ID]
			if previous.HookDigests[h.ID] == digest {
				if h.Enabled == nil || *h.Enabled {
					active++
				}
				continue
			}
			meta := IgnoredHook{Hook: h, Reason: "untrusted", Digest: digest}
			if previous.HookDigests[h.ID] != "" {
				meta.Reason = "changed since trust"
				meta.ChangedFiles = changedTrustFiles(filesForHook(files, h.ID), filesForHook(previous.Files, h.ID))
			}
			withheld[h.ID] = meta
		}
		for _, h := range p.Hooks {
			meta, exists := withheld[h.ID]
			if !exists {
				continue
			}
			gate := h.Phase == PhasePre && h.OnError == OnErrorDeny
			meta.GateBlocked = gate && (h.Enabled == nil || *h.Enabled) && (meta.Reason == "changed since trust" || active > 0)
			withheld[h.ID] = meta
			note := fmt.Sprintf("hook %s ignored (%s)", h.ID, meta.Reason)
			if gate {
				note += fmt.Sprintf("; gate hook %s inactive: %s", h.ID, meta.Reason)
			}
			if len(meta.ChangedFiles) > 0 {
				note += "; changed files: " + strings.Join(meta.ChangedFiles, ", ")
			}
			if meta.Reason == "changed since trust" && !bytes.Equal(previous.Definitions[h.ID], current.Definitions[h.ID]) {
				note += "; definition changed"
			}
			notes = append(notes, note+"; run ghok config trust")
		}
		if len(withheld) != 0 {
			state, _ := json.Marshal(withheld)
			ignored = &IgnoredProject{Path: canonical, Digest: contentHash(state)}
			ignored.ChangedFiles = changedTrustFiles(files, previous.Files)
			if len(previous.HookDigests) != 0 {
				comparison, _ := trustComparisonNotes(current, map[string]trustEntry{contentHash([]byte(canonical)): previous})
				notes = append(notes, comparison...)
			}
		}
	}
	status := "policy loaded; hooks trusted"
	if len(p.Hooks) == 0 {
		status = "policy loaded; no project hooks"
	} else if len(withheld) != 0 {
		status = "policy loaded; some hooks ignored; run ghok config trust"
	}
	notes[sourceIndex] = fmt.Sprintf("Project config: %s (%s)", project, status)
	merged := *c
	if err := mergeProject(&merged, p, n, project, cwd, env, &notes); err != nil {
		return ignoreProject(err)
	}
	if len(withheld) != 0 {
		active := make([]Hook, 0, len(merged.Hooks)-len(withheld))
		for _, h := range merged.Hooks {
			if meta, exists := withheld[h.ID]; exists && h.Source == project {
				meta.Hook = h
				merged.IgnoredHooks = append(merged.IgnoredHooks, meta)
			} else {
				active = append(active, h)
			}
		}
		merged.Hooks = active
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
	for i, h := range p.Hooks {
		command := h.Command[0]
		if !filepath.IsAbs(command) && !strings.HasPrefix(command, "./") {
			return nil, nil, fmt.Errorf("project config %s: hook %s command must use ./relative or an absolute path; bare PATH names are not allowed", project, h.ID)
		}
		for j, path := range h.Files {
			if !filepath.IsLocal(path) || filepath.IsAbs(path) || slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "..") || strings.ContainsAny(path, "*?[]") {
				return nil, nil, fmt.Errorf("hooks[%d].files[%d]: must be repository-relative with no .. or globs", i, j)
			}
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
	for _, k := range unknownKeys(n) {
		notes = append(notes, fmt.Sprintf("project config %s: line %d: unknown key %s ignored", project, k.line, k.name))
	}
	return notes
}

type unknownKey struct {
	line int
	name string
}

// unknownKeys walks the document against Config, including hooks list control
// items (parallel members are hooks; wait/wait-all have no nested keys).
func unknownKeys(n *yaml.Node) []unknownKey {
	var unknown []unknownKey
	seen := make(map[*yaml.Node]bool)
	var visit func(*yaml.Node, reflect.Type, string)
	visit = func(n *yaml.Node, t reflect.Type, path string) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		if t == reflect.TypeFor[HookList]() {
			t = reflect.TypeFor[hookPhases]()
		}
		switch n.Kind {
		case yaml.AliasNode:
			visit(n.Alias, t, path)
		case yaml.SequenceNode:
			for i, child := range n.Content {
				if t == reflect.TypeFor[hookItems]() {
					item := child
					if item.Kind == yaml.AliasNode {
						item = item.Alias
					}
					switch controlKey(item) {
					case "":
						visit(child, reflect.TypeFor[Hook](), fmt.Sprintf("%s[%d]", path, i))
					case "parallel":
						visit(field(item, "parallel"), t, fmt.Sprintf("%s[%d].parallel", path, i))
					}
				} else if t.Kind() == reflect.Slice {
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
					unknown = append(unknown, unknownKey{key.Line, name})
				}
			}
		}
	}
	visit(n, reflect.TypeFor[Config](), "")
	return unknown
}

func mergeProject(c, p *Config, n *yaml.Node, project, cwd string, env map[string]string, notes *[]string) error {
	// Each config file ends with an implicit wait-all: project hooks never
	// overlap user background hooks.
	for i := range p.Hooks {
		h := &p.Hooks[i]
		h.Wait = append(c.Hooks.backgroundIDs(h.Phase), h.Wait...)
		h.Source = project
		h.Command[0] = p.HookCommandPath(*h)
		warnWritable(hookPermissionPath(h.Command[0], cwd, env), notes)
		c.Hooks = append(c.Hooks, *h)
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
