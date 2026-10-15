package graphql

import (
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

// These are curated target semantics, not guesses from the mutation name.
// Regeneration must not silently redirect an existing action to another node.
func TestCuratedMutationSchemaTargets(t *testing.T) {
	targets := map[string][]string{
		"pullRequestId": {
			"mergePullRequest", "enablePullRequestAutoMerge", "disablePullRequestAutoMerge",
			"enqueuePullRequest", "addPullRequestReview", "submitPullRequestReview",
			"updatePullRequest", "closePullRequest", "reopenPullRequest", "revertPullRequest",
			"convertPullRequestToDraft", "markPullRequestReadyForReview", "requestReviews", "updatePullRequestBranch",
		},
		"repositoryId": {
			"createPullRequest", "createRef", "updateRefs", "mergeBranch", "createIssue",
			"archiveRepository", "unarchiveRepository", "updateRepository", "createBranchProtectionRule",
		},
		"id":                     {"dequeuePullRequest", "updateIssueComment", "deleteIssueComment", "updateIssue"},
		"refId":                  {"deleteRef", "updateRef"},
		"issueId":                {"closeIssue", "reopenIssue", "deleteIssue"},
		"subjectId":              {"addComment", "addReaction", "removeReaction", "minimizeComment", "unminimizeComment"},
		"lockableId":             {"lockLockable", "unlockLockable"},
		"branchProtectionRuleId": {"updateBranchProtectionRule", "deleteBranchProtectionRule"},
		"pullRequestReviewId":    {"dismissPullRequestReview", "deletePullRequestReview"},
		"starrableId":            {"addStar", "removeStar"},
		"":                       {"createRepository"},
	}
	covered := make(map[string]bool, len(mutationRegistry))
	for target, mutations := range targets {
		for _, mutation := range mutations {
			covered[mutation] = true
			t.Run(mutation, func(t *testing.T) {
				if _, curated := mutationRegistry[mutation]; !curated {
					t.Fatalf("target expectation has no curated mutation: %s", mutation)
				}
				schema, exists := schemaMutations[mutation]
				if !exists {
					t.Fatalf("curated mutation %s is absent from generated schema", mutation)
				}
				input := make(map[string]any, len(schema.IDFields))
				for _, candidate := range schema.IDFields {
					input[candidate.Name] = "node-for-" + candidate.Name
				}
				if target != "" {
					requireSchemaIDField(t, schema, target)
				}
				if mutation == "submitPullRequestReview" {
					requireSchemaIDField(t, schema, "pullRequestReviewId")
				}
				wantID := ""
				if target != "" {
					wantID = "node-for-" + target
				}
				op := mutationOperation(mutation, input)
				if op.Target.NodeID != wantID || op.Effect != model.EffectWrite || op.UnknownReason != model.UnknownNone {
					t.Fatalf("curated node target changed: want %q, got %+v", wantID, op)
				}
			})
		}
	}
	for mutation := range mutationRegistry {
		if !covered[mutation] {
			t.Errorf("curated mutation has no target expectation: %s", mutation)
		}
	}
}

func requireSchemaIDField(t *testing.T, schema mutationSchema, name string) {
	t.Helper()
	for _, field := range schema.InputFields {
		if field.Name == name {
			if field.Type != "ID" && field.Type != "ID!" {
				t.Fatalf("%s.%s is no longer a scalar ID: %s", schema.InputType, name, field.Type)
			}
			return
		}
	}
	t.Fatalf("curated ID field %s.%s is absent from generated input type", schema.InputType, name)
}
