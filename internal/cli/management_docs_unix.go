//go:build unix

package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkusaka/ghok"
	"github.com/mkusaka/ghok/internal/docsearch"
	"github.com/spf13/cobra"
)

func managementDocsCommand(code *int, version string) *cobra.Command {
	var asJSON, all bool
	docs := managementCommand("docs [id]", "Browse the embedded, offline documentation", code, func(_ *cobra.Command, args []string) int {
		index, err := docsearch.Load(ghok.Docs)
		if err != nil {
			return managementFailure(err)
		}
		if all {
			return managementDocsWrite(index.All())
		}
		if len(args) != 0 {
			section, ok := index.Lookup(args[0])
			if !ok {
				message := fmt.Sprintf("unknown docs id %q", args[0])
				if suggestions := index.Suggest(args[0]); len(suggestions) > 0 {
					message += "\nNearest ids: " + strings.Join(suggestions, ", ")
				}
				return managementFailure(fmt.Errorf("%s", message))
			}
			return managementDocsWrite(section.Markdown)
		}
		if asJSON {
			return managementJSON(index.Sections, nil)
		}
		for _, section := range index.Sections {
			if _, err := fmt.Fprintf(os.Stdout, "%s%s  %s — %s\n", strings.Repeat("  ", section.Depth), section.ID, section.Title, section.Description); err != nil {
				return managementFailure(err)
			}
		}
		return 0
	})
	docs.Args = func(cmd *cobra.Command, args []string) error {
		if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
			return err
		}
		if len(args) != 0 && (all || asJSON) {
			return fmt.Errorf("docs id cannot be combined with --all or --json")
		}
		return nil
	}
	docs.Flags().BoolVar(&asJSON, "json", false, "print the section tree as JSON")
	docs.Flags().BoolVar(&all, "all", false, "print every embedded file as Markdown")
	docs.MarkFlagsMutuallyExclusive("all", "json")
	docs.Example = "  ghok docs\n  ghok docs security/project-configuration-trust\n  ghok docs search \"parallel hooks\"\n  ghok docs --all"
	search := managementCommand("search <query>", "Search embedded documentation using an English query (JSON)", code, func(_ *cobra.Command, args []string) int {
		index, err := docsearch.Load(ghok.Docs)
		if err != nil {
			return managementFailure(err)
		}
		return managementJSON(index.Search(args[0], version, 5), nil)
	})
	search.Args = cobra.ExactArgs(1)
	search.Example = "  ghok docs search \"GH_TOKEN gain\""
	docs.AddCommand(search)
	return docs
}

func managementDocsWrite(markdown string) int {
	if _, err := fmt.Fprint(os.Stdout, markdown); err != nil {
		return managementFailure(err)
	}
	return 0
}
