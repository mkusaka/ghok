//go:build unix

package cli

import (
	"fmt"
	"os"

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
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(os.Stdout, r.String(line))
	}
	if err != nil {
		return managementFailure(err)
	}
	if all {
		_, _ = fmt.Fprintln(os.Stdout, "All project hook trust removed.")
	}
	return 0
}

func managementConfigTrust(command, path string, dryRun, noDiff bool) int {
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
		canonical, enabled, notes, diff, err = config.TrustProject(path, cwd, env, dryRun, noDiff)
	} else {
		canonical, notes, err = config.UntrustProject(path, cwd, env)
	}
	for _, note := range notes {
		_, _ = fmt.Fprintln(os.Stdout, r.String(note))
	}
	if len(diff) != 0 {
		_, _ = fmt.Fprintln(os.Stdout, "Content diff (unredacted trusted bytes → current files):")
		_, _ = os.Stdout.Write(diff)
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
		_, _ = fmt.Fprintln(os.Stdout, r.String(h.ID+" command: "+managementCompact(r.Argv(h.Command))))
	}
	if len(enabled) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No enabled project hooks.")
	}
	return 0
}
