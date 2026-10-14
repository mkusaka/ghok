package graphql

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

//go:generate go run ./generate -date 2026-10-14 -output schema_mutations.go

// The ID identifies the object being acted on, not necessarily a repository.
// Repository and host resolution are added by the document analyzer.
type mutationMapping struct {
	family string
	action string
	nodeID string
}

var mutationRegistry = map[string]mutationMapping{
	"mergePullRequest":              {"pr.merge", "pr.merge.direct", "pullRequestId"},
	"enablePullRequestAutoMerge":    {"pr.merge", "pr.merge.auto.enable", "pullRequestId"},
	"disablePullRequestAutoMerge":   {"pr.merge", "pr.merge.auto.disable", "pullRequestId"},
	"enqueuePullRequest":            {"pr.merge", "pr.merge.queue.enqueue", "pullRequestId"},
	"dequeuePullRequest":            {"pr.merge", "pr.merge.queue.dequeue", "id"},
	"addPullRequestReview":          {"pr.review", "pr.review", "pullRequestId"},
	"submitPullRequestReview":       {"pr.review", "pr.review", "pullRequestId"},
	"createPullRequest":             {"pr.create", "pr.create", "repositoryId"},
	"updatePullRequest":             {"pr.edit", "pr.edit", "pullRequestId"},
	"closePullRequest":              {"pr.close", "pr.close", "pullRequestId"},
	"reopenPullRequest":             {"pr.reopen", "pr.reopen", "pullRequestId"},
	"deleteRef":                     {"ref.delete", "ref.delete", "refId"},
	"createRef":                     {"ref.create", "ref.create", "repositoryId"},
	"updateRef":                     {"ref.update", "ref.update", "refId"},
	"updateRefs":                    {"ref.update", "ref.update.batch", "repositoryId"},
	"mergeBranch":                   {"branch.merge", "branch.merge", "repositoryId"},
	"createIssue":                   {"issue.create", "issue.create", "repositoryId"},
	"closeIssue":                    {"issue.close", "issue.close", "issueId"},
	"reopenIssue":                   {"issue.reopen", "issue.reopen", "issueId"},
	"deleteIssue":                   {"issue.delete", "issue.delete", "issueId"},
	"addComment":                    {"comment.create", "comment.create", "subjectId"},
	"updateIssueComment":            {"comment.edit", "comment.edit", "id"},
	"deleteIssueComment":            {"comment.delete", "comment.delete", "id"},
	"archiveRepository":             {"repo.archive", "repo.archive", "repositoryId"},
	"unarchiveRepository":           {"repo.unarchive", "repo.unarchive", "repositoryId"},
	"updateRepository":              {"repo.edit", "repo.edit", "repositoryId"},
	"createRepository":              {"repo.create", "repo.create", ""},
	"createBranchProtectionRule":    {"branch.protection", "branch.protection.create", "repositoryId"},
	"updateBranchProtectionRule":    {"branch.protection", "branch.protection.edit", "branchProtectionRuleId"},
	"deleteBranchProtectionRule":    {"branch.protection", "branch.protection.delete", "branchProtectionRuleId"},
	"revertPullRequest":             {"pr.revert", "pr.revert", "pullRequestId"},
	"convertPullRequestToDraft":     {"pr.draft", "pr.draft", "pullRequestId"},
	"markPullRequestReadyForReview": {"pr.ready", "pr.ready", "pullRequestId"},
	"requestReviews":                {"pr.review", "pr.review.request", "pullRequestId"},
	"dismissPullRequestReview":      {"pr.review", "pr.review.dismiss", "pullRequestReviewId"},
	"deletePullRequestReview":       {"pr.review", "pr.review.delete", "pullRequestReviewId"},
	"updatePullRequestBranch":       {"pr.update-branch", "pr.update-branch", "pullRequestId"},
	"addStar":                       {"star", "star.add", "starrableId"},
	"removeStar":                    {"star", "star.remove", "starrableId"},
}

func mutationOperation(field string, input map[string]any) model.Operation {
	// Preserve all values, including nested input and secrets; redaction belongs
	// to the output layer. Only merge operations need a copy for canonical keys.
	op := model.Operation{Effect: model.EffectWrite, Parameters: input}
	mapping, known := mutationRegistry[field]
	if !known {
		op.Family = model.FamilyUnknown
		op.Action = "graphql.mutation." + field
		op.UnknownReason = model.UnknownMutation
		if _, inSchema := schemaMutations[field]; inSchema {
			op.Detail = "known GitHub mutation without semantic mapping"
		} else {
			op.Detail = "not in schema snapshot (" + schemaSnapshotDate + ")"
		}
		return op
	}
	op.Family, op.Action = mapping.family, mapping.action
	idField := mapping.nodeID
	if field == "submitPullRequestReview" && inputString(input, "pullRequestId") == "" {
		idField = "pullRequestReviewId"
	}
	if idField != "" {
		op.Target.NodeID = inputString(input, idField)
		if op.Target.NodeID == "" {
			op.Unresolved = append(op.Unresolved, model.Unresolved{
				Field: "target.nodeId", Reason: "input." + idField + " is unavailable", Source: "graphql",
			})
		}
	}
	// These names are verified against the public schema, including the
	// distinct id field on dequeuePullRequest and refId on updateRef.
	op.Target.SHA = inputString(input, "expectedHeadOid")
	switch field {
	case "mergePullRequest", "enablePullRequestAutoMerge":
		canonicalMergeParameters(&op, input)
	case "addPullRequestReview", "submitPullRequestReview":
		canonicalReviewAction(&op, field, input)
	case "createPullRequest":
		op.Target.BaseBranch = inputString(input, "baseRefName")
		op.Target.HeadBranch = inputString(input, "headRefName")
		requireTarget(&op, "target.baseBranch", op.Target.BaseBranch, "baseRefName")
		requireTarget(&op, "target.headBranch", op.Target.HeadBranch, "headRefName")
	case "updatePullRequest":
		op.Target.BaseBranch = inputString(input, "baseRefName")
		if inputString(input, "state") == "CLOSED" {
			op.Detail = "updatePullRequest sets state CLOSED"
		}
	case "createRef":
		op.Target.Ref = inputString(input, "name")
		op.Target.SHA = inputString(input, "oid")
		requireTarget(&op, "target.ref", op.Target.Ref, "name")
		requireTarget(&op, "target.sha", op.Target.SHA, "oid")
	case "updateRef":
		op.Target.SHA = inputString(input, "oid")
		requireTarget(&op, "target.sha", op.Target.SHA, "oid")
	case "mergeBranch":
		op.Target.BaseBranch = inputString(input, "base")
		op.Target.HeadBranch = inputString(input, "head")
		requireTarget(&op, "target.baseBranch", op.Target.BaseBranch, "base")
		requireTarget(&op, "target.headBranch", op.Target.HeadBranch, "head")
	}
	return op
}

func inputString(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return value
}

func requireTarget(op *model.Operation, field, value, inputField string) {
	if value == "" {
		op.Unresolved = append(op.Unresolved, model.Unresolved{
			Field: field, Reason: "input." + inputField + " is unavailable", Source: "graphql",
		})
	}
}

func canonicalMergeParameters(op *model.Operation, input map[string]any) {
	op.Parameters = make(map[string]any, len(input)+3)
	for key, value := range input {
		op.Parameters[key] = value
	}
	method, exists := input["mergeMethod"]
	if !exists && input != nil {
		op.Parameters["method"] = "merge" // Schema default, not a guessed method.
	} else if value, ok := method.(string); ok && value != "" {
		op.Parameters["method"] = strings.ToLower(value)
	} else {
		op.Unresolved = append(op.Unresolved, model.Unresolved{
			Field: "parameters.method", Reason: "input.mergeMethod is unavailable", Source: "graphql",
		})
	}
	_, op.Parameters["commitHeadlinePresent"] = input["commitHeadline"]
	_, op.Parameters["commitBodyPresent"] = input["commitBody"]
}

func canonicalReviewAction(op *model.Operation, field string, input map[string]any) {
	event, exists := input["event"]
	if !exists && input != nil && field == "addPullRequestReview" {
		op.Action = "pr.review.pending"
		return
	}
	switch event {
	case "APPROVE":
		op.Action = "pr.review.approve"
	case "REQUEST_CHANGES":
		op.Action = "pr.review.request-changes"
	case "COMMENT":
		op.Action = "pr.review.comment"
	default:
		op.Unresolved = append(op.Unresolved, model.Unresolved{
			Field: "parameters.event", Reason: "review event is unavailable or not recognized", Source: "graphql",
		})
	}
}
