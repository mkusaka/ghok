package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mkusaka/ghok/internal/gitmeta"
)

// TrustMatch returns the key and original entry covering this checkout or its
// main worktree. Repositories match exactly; directories include descendants.
// root may also be a subdirectory of the checkout.
func TrustMatch(trust Trust, root string, env map[string]string) (key, entry string) {
	if len(trust.Repositories)+len(trust.Directories) == 0 {
		return "", ""
	}
	if checkout, err := projectRoot(root, ""); err == nil && checkout != "" {
		root = checkout
	}
	candidates := []string{canonicalPath(root)}
	if main := mainWorktree(root); main != "" && main != candidates[0] {
		candidates = append(candidates, main)
	}
	for _, paths := range []struct {
		key     string
		entries []string
	}{{"repositories", trust.Repositories}, {"directories", trust.Directories}} {
		for _, original := range paths.entries {
			path := canonicalPath(expandTrustPath(original, env["HOME"]))
			for _, candidate := range candidates {
				if candidate == path || paths.key == "directories" && pathWithin(path, candidate) {
					return paths.key, original
				}
			}
		}
	}
	return "", ""
}

func expandTrustPath(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func pathWithin(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && filepath.IsLocal(rel)
}

// canonicalPath cleans p and resolves symlinks when it exists.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// mainWorktree is the main working tree of a linked worktree at root: the
// parent of the shared common directory when that is a ".git" directory.
func mainWorktree(root string) string {
	// Like trust records, ignore GIT_DIR: this is about the checkout itself.
	gitdir, err := gitmeta.Directory(root, nil)
	if err != nil {
		return ""
	}
	data, err := gitmeta.ReadBounded(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return ""
	}
	common := canonicalPath(gitmeta.Absolute(gitdir, strings.TrimSpace(string(data))))
	if filepath.Base(common) != ".git" {
		return ""
	}
	if info, err := os.Stat(common); err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Dir(common)
}
