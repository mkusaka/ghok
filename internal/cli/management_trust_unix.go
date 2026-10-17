//go:build unix

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/redact"
)

func managementConfigTrustStore(all bool) int {
	env := environment()
	r := redact.New(env)
	var lines []string
	var err error
	if all {
		lines, err = config.UntrustAllProjects(env)
	} else {
		lines = config.ListProjectTrust(env)
		c, loadErr := config.Load(config.DefaultPath(env))
		if loadErr != nil {
			return managementFailure(loadErr)
		}
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return managementFailure(cwdErr)
		}
		key, entry := config.TrustMatch(c.Trust, cwd, env)
		for _, group := range []struct {
			key     string
			entries []string
		}{{"repositories", c.Trust.Repositories}, {"directories", c.Trust.Directories}} {
			lines = append(lines, "trust."+group.key+":")
			for _, value := range group.entries {
				line := fmt.Sprintf("  %q", value)
				if group.key == key && value == entry {
					line += " (matches here)"
				}
				lines = append(lines, line)
			}
		}
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(line)))
	}
	if err != nil {
		return managementFailure(err)
	}
	if all {
		_, _ = fmt.Fprintln(os.Stdout, "All project hook trust removed; trust.repositories/directories unchanged.")
	}
	return 0
}

func managementConfigTrust(command, path string, dryRun, noDiff, showSecrets bool, hookIDs []string) int {
	env := environment()
	r := redact.New(env)
	cwd, err := os.Getwd()
	if err != nil {
		return managementFailure(err)
	}
	var canonical string
	var notes []string
	var enabled []config.Hook
	var diff []byte
	if command == "trust" {
		canonical, enabled, notes, diff, err = config.TrustProject(path, cwd, env, dryRun, noDiff, hookIDs...)
	} else {
		canonical, notes, err = config.UntrustProject(path, cwd, env)
	}
	for _, note := range notes {
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(note)))
	}
	if len(diff) != 0 {
		// Like explain: credentials are masked for display (agents' transcripts
		// keep this output); the stored copies themselves stay byte-exact.
		if showSecrets {
			_, _ = fmt.Fprintln(os.Stdout, "Content diff (trusted bytes → current files, UNREDACTED):")
			_, _ = os.Stdout.Write(diff)
		} else {
			_, _ = fmt.Fprintln(os.Stdout, "Content diff (trusted bytes → current files; credentials masked, --show-secrets to reveal):")
			_, _ = fmt.Fprint(os.Stdout, r.String(string(diff)))
		}
	}
	if err != nil {
		return managementFailure(err)
	}
	if command == "untrust" {
		_, _ = fmt.Fprintln(os.Stdout, r.String("Project hook trust removed: "+canonical))
		return 0
	}
	status := "Project hooks trusted: "
	if dryRun {
		status = "Dry run; would trust project hooks: "
	}
	_, _ = fmt.Fprintln(os.Stdout, r.String(status+canonical))
	for _, h := range enabled {
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(h.ID+" command: "+managementCompact(r.Argv(h.Command)))))
	}
	if len(enabled) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No enabled project hooks.")
	}
	if !dryRun {
		managementTrustTip(canonical, cwd, env, r)
	}
	return 0
}

func managementConfigPathTrust(kind, path string, remove, dryRun bool) int {
	env := environment()
	r := redact.New(env)
	cwd, err := os.Getwd()
	if err != nil {
		return managementFailure(err)
	}
	entry, changed, diff, err := config.UpdatePathTrust(kind, path, cwd, env, remove, dryRun)
	if err != nil {
		return managementFailure(err)
	}
	if len(diff) != 0 {
		_, _ = fmt.Fprint(os.Stdout, r.String(string(diff)))
	}
	key := "repositories"
	if kind == "dir" {
		key = "directories"
	}
	status := "Added"
	if remove {
		status = "Removed"
	}
	if !changed {
		status = "Already trusted"
		if remove {
			status = "Already absent"
		}
	} else if dryRun {
		status = "Dry run; would add"
		if remove {
			status = "Dry run; would remove"
		}
	}
	_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(fmt.Sprintf("%s trust.%s %q", status, key, entry))))
	return 0
}

func managementTrustTip(project, cwd string, env map[string]string, r *redact.Redactor) {
	c, err := config.Load(config.DefaultPath(env))
	if err != nil {
		return
	}
	root := filepath.Dir(project)
	if key, _ := config.TrustMatch(c.Trust, root, env); key != "" {
		return
	}
	main, err := config.TrustRepositoryRoot(root, cwd)
	if err != nil {
		return
	}
	parent := filepath.Dir(main)
	prefix := ""
	home, err := filepath.EvalSymlinks(env["HOME"])
	if err == nil {
		if relative, relErr := filepath.Rel(home, parent); relErr == nil && filepath.IsLocal(relative) {
			prefix = "~/"
			parent = filepath.ToSlash(relative)
		}
	}
	if strings.ContainsAny(parent, " \t\n'\"\\$`;&|<>()*?[]{}!") {
		parent = "'" + strings.ReplaceAll(parent, "'", "'\\''") + "'"
	}
	parent = prefix + parent
	_, _ = fmt.Fprintln(os.Stdout, "Tip: trust this repository permanently: ghok config trust repo .")
	_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("     or every repository under its parent: ghok config trust dir "+parent)))
}
