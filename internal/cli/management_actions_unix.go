//go:build unix

package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/actions"
	"github.com/mkusaka/ghok/internal/normalize/graphql"
	"github.com/mkusaka/ghok/internal/normalize/native"
	"github.com/mkusaka/ghok/internal/normalize/rest"
	"github.com/spf13/cobra"
)

type managementActionFamily struct {
	Family  string                  `json:"family"`
	Actions []managementActionEntry `json:"actions"`
}

type managementActionEntry struct {
	Action  string       `json:"action"`
	Effect  model.Effect `json:"effect"`
	Native  []string     `json:"native"`
	REST    []string     `json:"rest"`
	GraphQL []string     `json:"graphql"`
}

func managementActionsCommand(code *int) *cobra.Command {
	var asJSON bool
	cmd := managementCommand("actions [prefix]", "List curated actions and their statically mapped routes", code, func(_ *cobra.Command, args []string) int {
		prefix := ""
		if len(args) != 0 {
			prefix = args[0]
		}
		families := managementActionsCatalogue(prefix)
		if asJSON {
			return managementJSON(families, nil)
		}
		if _, err := fmt.Fprint(os.Stdout, managementActionsText(families)); err != nil {
			return managementFailure(err)
		}
		return 0
	})
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func managementActionsCatalogue(prefix string) []managementActionFamily {
	nativeRoutes, restRoutes, graphqlRoutes := native.ActionRoutes(), rest.ActionRoutes(), graphql.ActionRoutes()
	byFamily := make(map[string][]managementActionEntry)
	for _, spec := range actions.All() {
		if !strings.HasPrefix(spec.Action, prefix) {
			continue
		}
		byFamily[spec.Family] = append(byFamily[spec.Family], managementActionEntry{
			Action: spec.Action, Effect: spec.Effect,
			Native:  managementActionRoutes(nativeRoutes[spec.Action]),
			REST:    managementActionRoutes(restRoutes[spec.Action]),
			GraphQL: managementActionRoutes(graphqlRoutes[spec.Action]),
		})
	}
	families := make([]string, 0, len(byFamily))
	for family := range byFamily {
		families = append(families, family)
	}
	slices.Sort(families)
	catalogue := make([]managementActionFamily, 0, len(families))
	for _, family := range families {
		catalogue = append(catalogue, managementActionFamily{Family: family, Actions: byFamily[family]})
	}
	return catalogue
}

func managementActionRoutes(routes []string) []string {
	if routes == nil {
		return []string{}
	}
	slices.Sort(routes)
	return slices.Compact(routes)
}

func managementActionsText(families []managementActionFamily) string {
	var text strings.Builder
	for _, family := range families {
		fmt.Fprintln(&text, family.Family)
		for _, action := range family.Actions {
			fmt.Fprintf(&text, "  %s (%s)\n", action.Action, action.Effect)
			for _, route := range action.Native {
				fmt.Fprintf(&text, "    native: %s\n", route)
			}
			for _, route := range action.REST {
				fmt.Fprintf(&text, "    rest: %s\n", route)
			}
			for _, route := range action.GraphQL {
				fmt.Fprintf(&text, "    graphql: %s\n", route)
			}
		}
	}
	return text.String()
}
