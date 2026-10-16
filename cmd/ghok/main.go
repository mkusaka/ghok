// Command ghok is both the ghok management CLI and, when invoked as "gh",
// a transparent wrapper around the real GitHub CLI.
package main

import (
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/mkusaka/ghok/internal/cli"
)

var version = "dev"
var commit string

func resolveBuildInfo(version, commit string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return version, commit
	}
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	if commit == "" {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				commit = setting.Value
				break
			}
		}
	}
	return version, commit
}

func main() {
	info, _ := debug.ReadBuildInfo()
	resolvedVersion, resolvedCommit := resolveBuildInfo(version, commit, info)
	if filepath.Base(os.Args[0]) == "gh" {
		cli.Version = resolvedVersion
		os.Exit(cli.Wrapper(os.Args[1:]))
	}
	os.Exit(cli.Management(os.Args[1:], resolvedVersion, resolvedCommit))
}
