package rest

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestOverrideContract(t *testing.T) {
	generated := make(map[string]bool)
	for _, r := range generatedRoutes {
		if generated[r.operationID] {
			t.Fatalf("duplicate generated operationId: %s", r.operationID)
		}
		generated[r.operationID] = true
	}
	ids := make([]string, 0, len(routeOverrides))
	actions := make(map[string][]string)
	for id, override := range routeOverrides {
		if !generated[id] {
			t.Errorf("override operationId missing from OpenAPI: %s", id)
		}
		if override.family == "" || override.action == "" || strings.HasPrefix(override.action, "rest.") {
			t.Errorf("invalid canonical override: %s: %+v", id, override)
		}
		ids = append(ids, id)
		actions[override.action] = append(actions[override.action], id)
	}
	// These legacy names intentionally span scopes or equivalent list routes.
	// All other canonical actions must be unique; new sharing needs review.
	shared := map[string][]string{
		"hook.create":            {"orgs/create-webhook", "repos/create-webhook"},
		"hook.delete":            {"orgs/delete-webhook", "repos/delete-webhook"},
		"hook.update":            {"orgs/update-webhook", "repos/update-webhook"},
		"issue.comments.list":    {"issues/list-comments", "issues/list-comments-for-repo"},
		"notification.mark_read": {"activity/mark-notifications-as-read", "activity/mark-repo-notifications-as-read", "activity/mark-thread-as-read"},
		"pr.comments.list":       {"pulls/list-review-comments", "pulls/list-review-comments-for-repo"},
		"repo.create":            {"repos/create-for-authenticated-user", "repos/create-in-org"},
		"repo.list":              {"repos/list-for-authenticated-user", "repos/list-for-org", "repos/list-for-user"},
		"secret.delete":          {"actions/delete-environment-secret", "actions/delete-org-secret", "actions/delete-repo-secret"},
		"secret.list":            {"actions/list-org-secrets", "actions/list-repo-secrets"},
		"secret.put":             {"actions/create-or-update-environment-secret", "actions/create-or-update-org-secret", "actions/create-or-update-repo-secret"},
		"variable.create":        {"actions/create-environment-variable", "actions/create-org-variable", "actions/create-repo-variable"},
		"variable.delete":        {"actions/delete-environment-variable", "actions/delete-org-variable", "actions/delete-repo-variable"},
		"variable.update":        {"actions/update-environment-variable", "actions/update-org-variable", "actions/update-repo-variable"},
	}
	for action, operationIDs := range actions {
		slices.Sort(operationIDs)
		if len(operationIDs) > 1 && !slices.Equal(operationIDs, shared[action]) {
			t.Errorf("unreviewed action collision %s: %v", action, operationIDs)
		}
	}
	for action, operationIDs := range shared {
		if !slices.Equal(actions[action], operationIDs) {
			t.Errorf("shared canonical action changed %s: %v", action, actions[action])
		}
	}
	slices.Sort(ids)
	var contract strings.Builder
	for _, id := range ids {
		override := routeOverrides[id]
		fmt.Fprintf(&contract, "%s\t%s\t%s\n", id, override.family, override.action)
	}
	// Policy/hook identities are a compatibility contract, not generated names.
	const want = "67d3c273da1eab4f32f8c165a86a4c49d0bd388e361578ea883012d9249318d6"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(contract.String()))); got != want {
		t.Fatalf("canonical family/action contract changed: %s; review before updating the digest", got)
	}
}

func TestGeneratedClassifications(t *testing.T) {
	for _, tc := range []struct {
		method, path, family, action string
		effect                       model.Effect
	}{
		{"GET", "/", "rest.meta", "rest.meta.root", model.EffectRead},
		{"GET", "https://api.github.com", "rest.meta", "rest.meta.root", model.EffectRead},
		{"GET", "search/issues?q=bug", "rest.search", "rest.search.issues-and-pull-requests", model.EffectRead},
		{"POST", "user/codespaces", "rest.codespaces", "rest.codespaces.create-for-authenticated-user", model.EffectWrite},
		{"DELETE", "user/codespaces/example-space", "rest.codespaces", "rest.codespaces.delete-for-authenticated-user", model.EffectWrite},
		{"PATCH", "repos/o/r/code-scanning/alerts/12", "rest.code-scanning", "rest.code-scanning.update-alert", model.EffectWrite},
		{"GET", "repos/o/r/pages/deployments/opaque-build", "rest.repos", "rest.repos.get-pages-deployment", model.EffectRead},
		{"POST", "repos/o/r/merge-upstream", "branch.merge", "branch.merge-upstream", model.EffectWrite},
		{"GET", "repos/o/r/contents", "file.read", "file.list", model.EffectRead},
		{"GET", "repos/o/r/contents/a/b", "file.read", "file.view", model.EffectRead},
		{"PATCH", "gists/abc123", "gist.update", "gist.update", model.EffectWrite},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			res := classify(t, knownInv(), "-X", tc.method, tc.path)
			requireOperation(t, res, tc.family, tc.action, tc.effect)
			if res.Classification.Confidence != model.ConfidenceExact {
				t.Fatal(res.Classification)
			}
		})
	}
}

func TestGeneratedRouteBoundaries(t *testing.T) {
	for _, path := range []string{
		"repos/o/r/code-scanning/alerts/not-a-number",
		"repos/o/r/code-scanning/alerts/0",
		"repos/o/r/code-scanning/alerts/999999999999999999999999",
		"repos/o/r/code-scanning/alerts/12/extra",
		"repos/o/r/code-scanning/alerts/12-extra",
		"repos/o%2Fx/r/code-scanning/alerts/12",
	} {
		res := classify(t, knownInv(), "-XPATCH", path)
		op := requireOperation(t, res, model.FamilyUnknown, "api.patch.unknown", model.EffectWrite)
		if op.UnknownReason != model.UnknownEndpoint || res.Classification.Confidence != model.ConfidenceNone {
			t.Fatal(res)
		}
	}
	// A generated deprecated operation is still a write, not a safety exception.
	res := classify(t, knownInv(), "-XDELETE", "teams/12")
	requireOperation(t, res, "rest.teams", "rest.teams.delete-legacy", model.EffectWrite)
	// GHES-only operations absent from the public snapshot stay unknown.
	res = classify(t, knownInv(), "-XPOST", "https://enterprise.example/api/v3/admin/users")
	requireOperation(t, res, model.FamilyUnknown, "api.post.unknown", model.EffectWrite)
}

func TestGeneratedTargetsAndLiteralPrecedence(t *testing.T) {
	res := classify(t, knownInv(), "repos/o/r/commits/feature%2Ftopic")
	op := requireOperation(t, res, "rest.repos", "rest.repos.get-commit", model.EffectRead)
	if op.Target.Owner != "o" || op.Target.Repository != "o/r" || op.Target.Ref != "feature/topic" {
		t.Fatal(op.Target)
	}
	res = classify(t, knownInv(), "repos/o/r/git/trees/abc123")
	op = requireOperation(t, res, "rest.git", "rest.git.get-tree", model.EffectRead)
	if op.Target.SHA != "abc123" || op.Target.Repository != "o/r" {
		t.Fatal(op.Target)
	}
	// /issues/comments must beat the integer issue_number capture.
	res = classify(t, knownInv(), "repos/o/r/issues/comments")
	requireOperation(t, res, "issue.read", "issue.comments.list", model.EffectRead)
	// Both paths accept strings; literal precedence must not rely on type rejection.
	res = classify(t, knownInv(), "agents/automations/available-tools")
	op = requireOperation(t, res, "rest.agent-automations", "rest.agent-automations.list-available-automation-tools", model.EffectRead)
	if op.Parameters["automation_id"] != nil {
		t.Fatal(op.Parameters)
	}
}
