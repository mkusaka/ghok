package graphql

import (
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/actions"
)

func TestCuratedActionCatalogue(t *testing.T) {
	for field, mapping := range mutationRegistry {
		if spec, ok := actions.Lookup(mapping.Action); !ok || spec != mapping.Spec {
			t.Errorf("%s has an uncurated identity: %+v", field, mapping)
		}
	}
}

func TestPolymorphicTargetKinds(t *testing.T) {
	for _, tc := range []struct{ field, id, family, action string }{
		{"addComment", "subjectId", "comment", "comment.create"},
		{"addReaction", "subjectId", "reaction", "reaction.add"},
		{"removeReaction", "subjectId", "reaction", "reaction.remove"},
		{"lockLockable", "lockableId", "lockable", "lockable.lock"},
		{"unlockLockable", "lockableId", "lockable", "lockable.unlock"},
		{"minimizeComment", "subjectId", "comment", "comment.minimize"},
		{"unminimizeComment", "subjectId", "comment", "comment.unminimize"},
		{"addStar", "starrableId", "star", "star.add"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			// Prefixes and opaque values cannot establish a GraphQL __typename.
			for _, id := range []string{"PR_fake", "ISSUE_fake", "opaque", ""} {
				op := mutationOperation(tc.field, map[string]any{tc.id: id})
				if op.Action != tc.action || op.Family != tc.family || op.Effect != model.EffectWrite || op.Target.Kind != "unknown" || op.Target.NodeID != id {
					t.Errorf("%s(%q) = %+v", tc.field, id, op)
				}
				if !hasKindUnresolved(op) {
					t.Errorf("missing subject-kind uncertainty: %+v", op)
				}
			}
		})
	}
}

func TestKnownTargetKinds(t *testing.T) {
	for _, tc := range []struct{ field, id, kind string }{
		{"mergePullRequest", "pullRequestId", "pullRequest"},
		{"dequeuePullRequest", "id", "pullRequest"},
		{"closeIssue", "issueId", "issue"},
		{"updateIssue", "id", "issue"},
		{"createPullRequest", "repositoryId", "repository"},
		{"createRef", "repositoryId", "repository"},
		{"updateRef", "refId", "ref"},
		{"deleteRef", "refId", "ref"},
		{"updateIssueComment", "id", "comment"},
		{"deleteIssueComment", "id", "comment"},
		{"submitPullRequestReview", "pullRequestReviewId", "pullRequestReview"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			op := mutationOperation(tc.field, map[string]any{tc.id: "opaque"})
			if op.Target.Kind != tc.kind || op.Target.NodeID != "opaque" || hasKindUnresolved(op) {
				t.Fatalf("known target kind = %+v; want %s", op, tc.kind)
			}
		})
	}
}

func hasKindUnresolved(op model.Operation) bool {
	for _, u := range op.Unresolved {
		if u.Field == "target.kind" && u.Source == "api" {
			return true
		}
	}
	return false
}
