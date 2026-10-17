package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mkusaka/ghok/internal/gitmeta"
)

// trustSkipMatch returns the user trust.skip pattern covering the repository at
// root, or "". Like an IDE's trusted parent folder, a pattern ending in "/"
// covers everything below it; otherwise the path must match exactly. Linked
// git worktrees also match through their main worktree.
func trustSkipMatch(patterns []string, root string, env map[string]string) string {
	if len(patterns) == 0 {
		return ""
	}
	candidates := []string{canonicalPath(root)}
	if main := mainWorktree(root); main != "" && main != candidates[0] {
		candidates = append(candidates, main)
	}
	for _, pattern := range patterns {
		prefix := strings.HasSuffix(pattern, "/")
		path := pattern
		if path == "~" || strings.HasPrefix(path, "~/") {
			path = filepath.Join(env["HOME"], strings.TrimPrefix(path, "~"))
		}
		path = canonicalPath(path)
		for _, candidate := range candidates {
			if candidate == path || prefix && strings.HasPrefix(candidate, path+string(filepath.Separator)) {
				return pattern
			}
		}
	}
	return ""
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
