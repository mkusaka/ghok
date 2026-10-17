package config

import (
	"errors"
	"os"
)

// HasConfig checks only config presence, without loading YAML or trust state.
// Lookup failures take the normal loader path so invalid config cannot bypass it.
func HasConfig(cwd string, env map[string]string) bool {
	if _, err := os.Stat(DefaultPath(env)); !errors.Is(err, os.ErrNotExist) {
		return true
	}
	project, err := findProject(cwd, env["HOME"])
	if project != "" || err != nil {
		return true
	}
	local, err := localPath(cwd, env["HOME"])
	return local != "" || err != nil
}

// IgnoredProject identifies the manifest whose enabled hooks were withheld.
// It is reporting metadata, not an executable hook selection.
type IgnoredProject struct {
	Path         string
	Digest       string
	ChangedFiles []string
}
