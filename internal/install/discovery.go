// Package install discovers real gh and manages explicitly owned PATH shims.
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DiscoverOptions struct {
	PathEnv  string
	Self     string
	Override string
}

type Candidate struct {
	Path     string
	Resolved string
	Skipped  string
}

// FindRealGh returns the first usable PATH-facing path, not its resolved target.
// Retaining the symlink path allows Homebrew upgrades without pinning a Cellar.
// All entries, including duplicate installations, are returned for diagnostics.
func FindRealGh(o DiscoverOptions) (string, []Candidate, error) {
	self, err := os.Stat(o.Self)
	if err != nil {
		return "", nil, fmt.Errorf("identify ghok executable %q: %w", o.Self, err)
	}
	if o.Override != "" {
		c := inspectCandidate(o.Override, self)
		if !filepath.IsAbs(o.Override) {
			c.Skipped = "override must be an absolute path"
		}
		if c.Skipped != "" {
			return "", []Candidate{c}, fmt.Errorf("invalid real gh override %q: %s", o.Override, c.Skipped)
		}
		return c.Path, []Candidate{c}, nil
	}
	var candidates []Candidate
	var first string
	// Split rather than SplitList: even an entirely empty PATH denotes '.'.
	for _, dir := range strings.Split(o.PathEnv, string(os.PathListSeparator)) {
		if !filepath.IsAbs(dir) {
			candidates = append(candidates, Candidate{Path: filepath.Join(dir, "gh"), Skipped: "relative or empty PATH entry skipped"})
			continue
		}
		c := inspectCandidate(filepath.Join(dir, "gh"), self)
		candidates = append(candidates, c)
		if first == "" && c.Skipped == "" {
			first = c.Path
		}
	}
	if first != "" {
		return first, candidates, nil
	}
	var detail strings.Builder
	for _, c := range candidates {
		fmt.Fprintf(&detail, "; %s: %s", c.Path, c.Skipped)
	}
	return "", candidates, fmt.Errorf("no real gh executable found on PATH%s", detail.String())
}

func inspectCandidate(path string, self os.FileInfo) Candidate {
	c := Candidate{Path: path}
	if _, err := os.Lstat(path); err != nil {
		c.Skipped = err.Error()
		return c
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "too many links") || strings.Contains(message, "too many levels") {
			c.Skipped = "symlink loop: " + err.Error()
		} else {
			c.Skipped = "cannot resolve symlink: " + err.Error()
		}
		return c
	}
	c.Resolved = resolved
	info, err := os.Stat(resolved)
	switch {
	case err != nil:
		c.Skipped = err.Error()
	case self != nil && os.SameFile(info, self), filepath.Base(resolved) == "ghok":
		c.Skipped = "ghok shim"
	case !info.Mode().IsRegular():
		c.Skipped = "not a regular file"
	case info.Mode().Perm()&0111 == 0:
		c.Skipped = "not executable"
	}
	return c
}
