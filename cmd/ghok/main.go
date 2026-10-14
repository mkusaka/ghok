// Command ghok is both the ghok management CLI and, when invoked as "gh",
// a transparent wrapper around the real GitHub CLI.
package main

import (
	"os"
	"path/filepath"

	"github.com/mkusaka/ghok/internal/cli"
)

var version = "dev"
var commit string

func main() {
	if filepath.Base(os.Args[0]) == "gh" {
		os.Exit(cli.Wrapper(os.Args[1:]))
	}
	os.Exit(cli.Management(os.Args[1:], version, commit))
}
