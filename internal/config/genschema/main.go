// Command genschema writes the JSON Schemas for ghok's config files:
//
//	go run ./internal/config/genschema [-dir schema]
//
// It is invoked by `go generate ./internal/config` and `make schema`.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mkusaka/ghok/internal/config"
)

func main() {
	dir := flag.String("dir", "schema", "output directory")
	flag.Parse()
	for name, project := range map[string]bool{"config.schema.json": false, "project.schema.json": true} {
		path := filepath.Join(*dir, name)
		if err := os.WriteFile(path, config.SchemaJSON(project), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "genschema:", err)
			os.Exit(1)
		}
	}
}
