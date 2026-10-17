//go:build unix

package cli

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/actions"
)

func TestManagementActionsCatalogueIdentities(t *testing.T) {
	catalogue := managementActionsCatalogue("")
	seen := make(map[string]bool)
	for i, family := range catalogue {
		if i > 0 && catalogue[i-1].Family >= family.Family {
			t.Errorf("families not sorted: %s, %s", catalogue[i-1].Family, family.Family)
		}
		if len(family.Actions) == 0 {
			t.Errorf("empty family: %s", family.Family)
		}
		for j, entry := range family.Actions {
			spec, ok := actions.Lookup(entry.Action)
			if !ok || spec.Family != family.Family || spec.Effect != entry.Effect {
				t.Errorf("identity mismatch: family=%s entry=%+v spec=%+v", family.Family, entry, spec)
			}
			if seen[entry.Action] {
				t.Errorf("duplicate action: %s", entry.Action)
			}
			seen[entry.Action] = true
			if j > 0 && family.Actions[j-1].Action >= entry.Action {
				t.Errorf("actions not sorted in family %s", family.Family)
			}
			for _, routes := range [][]string{entry.Native, entry.REST, entry.GraphQL} {
				if routes == nil || !slices.IsSorted(routes) {
					t.Errorf("nil or unsorted routes for %s: %v", entry.Action, routes)
				}
				for k := 1; k < len(routes); k++ {
					if routes[k-1] == routes[k] {
						t.Errorf("duplicate route for %s: %s", entry.Action, routes[k])
					}
				}
			}
		}
	}
	for _, spec := range actions.All() {
		if !seen[spec.Action] {
			t.Errorf("missing curated action: %s", spec.Action)
		}
	}
	if len(seen) != len(actions.All()) {
		t.Errorf("listed %d actions, want %d", len(seen), len(actions.All()))
	}
	for range 3 {
		if got := managementActionsCatalogue(""); !reflect.DeepEqual(got, catalogue) {
			t.Fatal("catalogue order changed between calls")
		}
	}
}

func TestManagementActionsPrefix(t *testing.T) {
	for _, prefix := range []string{"pr.merge", "comment.create", "issue.comment", "pr", "does-not-exist"} {
		t.Run(prefix, func(t *testing.T) {
			seen := make(map[string]bool)
			for _, family := range managementActionsCatalogue(prefix) {
				for _, action := range family.Actions {
					if !strings.HasPrefix(action.Action, prefix) {
						t.Errorf("unmatched action: %s", action.Action)
					}
					seen[action.Action] = true
				}
			}
			for _, spec := range actions.All() {
				if want := strings.HasPrefix(spec.Action, prefix); seen[spec.Action] != want {
					t.Errorf("action %s present=%t, want %t", spec.Action, seen[spec.Action], want)
				}
			}
		})
	}
	catalogue := managementActionsCatalogue("pr.merge")
	if len(catalogue) != 2 || catalogue[0].Family != "pr.merge" || catalogue[1].Family != "pr.read" {
		t.Fatalf("prefix must preserve exact families, including merge status: %+v", catalogue)
	}
}

func TestManagementActionsStaticRoutes(t *testing.T) {
	for _, tc := range []struct {
		action  string
		native  []string
		rest    []string
		graphql []string
	}{
		{
			action:  "comment.create",
			rest:    []string{"POST /repos/{owner}/{repo}/issues/{issue_number}/comments"},
			graphql: []string{"addComment"},
		},
		{
			action: "issue.comment.create",
			native: []string{"gh issue comment"},
		},
		{
			action: "pr.comment.create",
			native: []string{"gh pr comment"},
			rest:   []string{"POST /repos/{owner}/{repo}/pulls/{pull_number}/comments"},
		},
		{
			action: "pr.merge.request",
			native: []string{"gh pr merge"},
		},
		{
			action:  "pr.merge.direct",
			rest:    []string{"PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge"},
			graphql: []string{"mergePullRequest"},
		},
		{action: "pr.merge.auto.enable", graphql: []string{"enablePullRequestAutoMerge"}},
		{action: "pr.merge.auto.disable", graphql: []string{"disablePullRequestAutoMerge"}},
		{action: "pr.merge.queue.enqueue", graphql: []string{"enqueuePullRequest"}},
		{action: "pr.merge.queue.dequeue", graphql: []string{"dequeuePullRequest"}},
		{
			action:  "pr.review",
			native:  []string{"gh pr review"},
			graphql: []string{"addPullRequestReview", "submitPullRequestReview"},
		},
		{
			action: "pr.review.create",
			rest:   []string{"POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews"},
		},
		{action: "pr.review.approve"},
		{action: "pr.review.comment"},
		{action: "pr.review.request-changes"},
		{action: "pr.review.pending"},
		{action: "issue.comment.edit"},
		{action: "issue.comment.delete"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			var entry managementActionEntry
			found := false
			for _, family := range managementActionsCatalogue(tc.action) {
				for _, candidate := range family.Actions {
					if candidate.Action == tc.action {
						entry, found = candidate, true
					}
				}
			}
			if !found {
				t.Fatalf("missing action %s", tc.action)
			}
			if entry.Effect != model.EffectWrite {
				t.Errorf("effect=%s, want write", entry.Effect)
			}
			if !slices.Equal(entry.Native, tc.native) || !slices.Equal(entry.REST, tc.rest) || !slices.Equal(entry.GraphQL, tc.graphql) {
				t.Errorf("routes for %s: native=%v rest=%v graphql=%v; want native=%v rest=%v graphql=%v", tc.action, entry.Native, entry.REST, entry.GraphQL, tc.native, tc.rest, tc.graphql)
			}
		})
	}
}
