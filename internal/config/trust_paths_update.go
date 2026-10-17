package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mkusaka/ghok/internal/textdiff"
	"go.yaml.in/yaml/v3"
)

// TrustRepositoryRoot resolves an existing directory to its checkout root,
// preferring the main checkout when it is a linked worktree.
func TrustRepositoryRoot(path, cwd string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = expandTrustPath(path, home)
	}
	path, err := trustDirectory(path, cwd)
	if err != nil {
		return "", err
	}
	root, err := projectRoot(path, "")
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("%s is not inside a repository", path)
	}
	if main := mainWorktree(root); main != "" {
		return main, nil
	}
	return root, nil
}

func trustDirectory(path, cwd string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return filepath.EvalSymlinks(path)
}

// UpdatePathTrust edits only the selected user trust list. It preserves YAML
// comments and the config file's symlink and mode; dry runs never write.
func UpdatePathTrust(kind, path, cwd string, env map[string]string, remove, dryRun bool) (entry string, changed bool, diff []byte, err error) {
	key := ""
	switch kind {
	case "repo":
		key = "repositories"
	case "dir":
		key = "directories"
	default:
		return "", false, nil, fmt.Errorf("unknown trust path kind %q", kind)
	}
	configPath := DefaultPath(env)
	target, err := filepath.EvalSymlinks(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil, errors.New("user config does not exist; run `ghok config init --scope user` first")
	}
	if err != nil {
		return "", false, nil, err
	}
	if _, err := Load(configPath); err != nil {
		return "", false, nil, err
	}
	before, err := os.ReadFile(target)
	if err != nil {
		return "", false, nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", false, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(before, &doc); err != nil {
		return "", false, nil, err
	}
	var n *yaml.Node
	if len(doc.Content) != 0 {
		n = doc.Content[0]
	}
	trust := field(n, "trust")
	if trust != directTrustField(n, "trust") || unsafeTrustNode(trust) {
		return "", false, nil, errors.New("cannot edit trust reached through YAML aliases or merge keys")
	}
	resolved, err := trustDirectory(expandTrustPath(path, env["HOME"]), cwd)
	if err != nil {
		return "", false, nil, err
	}
	if kind == "repo" {
		resolved, err = TrustRepositoryRoot(resolved, cwd)
		if err != nil {
			return "", false, nil, err
		}
	}
	entry = resolved
	if home := env["HOME"]; home != "" {
		home = canonicalPath(home)
		if pathWithin(home, resolved) {
			rel, _ := filepath.Rel(home, resolved)
			entry = "~/"
			if rel != "." {
				entry += filepath.ToSlash(rel)
			}
		}
	}
	list := directTrustField(trust, key)
	var kept []*yaml.Node
	found := false
	if list != nil {
		for _, item := range list.Content {
			if canonicalPath(expandTrustPath(item.Value, env["HOME"])) == resolved {
				if remove || found {
					changed = true
					continue
				}
				found = true
			}
			kept = append(kept, item)
		}
	}
	if !remove && !found {
		kept = append(kept, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry})
		changed = true
	}
	if !changed {
		return entry, false, nil, nil
	}
	if n == nil {
		n = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	} else if n.Tag == "!!null" {
		if n.LineComment != "" {
			n.HeadComment = strings.TrimSpace(n.HeadComment + "\n" + n.LineComment)
			n.LineComment = ""
		}
		n.Kind, n.Tag, n.Value = yaml.MappingNode, "!!map", ""
	}
	// Empty configs may omit version, but the new non-empty mapping may not.
	if field(n, "version") == nil {
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	}
	if trust == nil {
		trust = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "trust"}, trust)
	} else if trust.Tag == "!!null" {
		trust.Kind, trust.Tag, trust.Value = yaml.MappingNode, "!!map", ""
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		trust.Content = append(trust.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, list)
	}
	list.Kind, list.Tag, list.Value, list.Content = yaml.SequenceNode, "!!seq", "", kept
	doc.Kind, doc.Content = yaml.DocumentNode, []*yaml.Node{n}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return entry, false, nil, err
	}
	if err := encoder.Close(); err != nil {
		return entry, false, nil, err
	}
	after := buffer.Bytes()
	if err := sameNonPathTrust(before, after, filepath.Dir(configPath)); err != nil {
		return entry, false, nil, err
	}
	diff = textdiff.Diff(configPath, before, configPath, after)
	if dryRun {
		return entry, true, diff, nil
	}
	if err := writePathTrust(target, after, info.Mode()); err != nil {
		return entry, false, nil, err
	}
	// Re-read the published file, including strict user-scope validation. Never
	// leave a syntactically valid edit that changed unrelated raw YAML semantics.
	_, loadErr := Load(configPath)
	saved, readErr := os.ReadFile(target)
	var semanticErr error
	if readErr == nil {
		semanticErr = sameNonPathTrust(before, saved, filepath.Dir(configPath))
		if semanticErr == nil && !bytes.Equal(saved, after) {
			semanticErr = errors.New("saved config differs from the requested edit")
		}
	}
	if err := errors.Join(loadErr, readErr, semanticErr); err != nil {
		restoreErr := writePathTrust(target, before, info.Mode())
		return entry, false, nil, errors.Join(err, restoreErr)
	}
	return entry, true, diff, nil
}

func directTrustField(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func unsafeTrustNode(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return true
	}
	for _, child := range n.Content {
		if unsafeTrustNode(child) {
			return true
		}
	}
	return false
}

func sameNonPathTrust(before, after []byte, dir string) error {
	var values [2]map[string]any
	var configs [2]*Config
	for i, data := range [][]byte{before, after} {
		c, err := Parse(data, dir)
		if err != nil {
			return err
		}
		c.Trust.Repositories, c.Trust.Directories = nil, nil
		configs[i] = c
		if err := yaml.Unmarshal(data, &values[i]); err != nil {
			return err
		}
		if values[i] == nil {
			values[i] = make(map[string]any)
		}
		// This is the only raw default normalization: an empty config gains
		// the required version without changing its effective meaning.
		if _, exists := values[i]["version"]; !exists {
			values[i]["version"] = DefaultVersion
		}
		if trust, ok := values[i]["trust"].(map[string]any); ok {
			delete(trust, "repositories")
			delete(trust, "directories")
			if len(trust) == 0 {
				delete(values[i], "trust")
			}
		} else if values[i]["trust"] == nil {
			delete(values[i], "trust")
		}
	}
	if !reflect.DeepEqual(values[0], values[1]) || !reflect.DeepEqual(configs[0], configs[1]) {
		return errors.New("refusing to change config semantics outside trust.repositories/trust.directories")
	}
	return nil
}

func writePathTrust(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".config-trust-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
