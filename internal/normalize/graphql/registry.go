package graphql

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

//go:generate go run ./generate -date 2026-10-15 -sha256 3ed095654b22b1a8e7a88af36af2b28cb6d9613da332e9c57303e30476958d42 -output schema_mutations.go

// Curated identities override the generated defaults. nodeID is only needed
// when the curated target differs from the schema's primary ID selection.
type mutationMapping struct {
	family string
	action string
	nodeID string
}

var mutationRegistry = map[string]mutationMapping{
	"mergePullRequest":              {"pr.merge", "pr.merge.direct", ""},
	"enablePullRequestAutoMerge":    {"pr.merge", "pr.merge.auto.enable", ""},
	"disablePullRequestAutoMerge":   {"pr.merge", "pr.merge.auto.disable", ""},
	"enqueuePullRequest":            {"pr.merge", "pr.merge.queue.enqueue", ""},
	"dequeuePullRequest":            {"pr.merge", "pr.merge.queue.dequeue", ""},
	"addPullRequestReview":          {"pr.review", "pr.review", ""},
	"submitPullRequestReview":       {"pr.review", "pr.review", "pullRequestId"},
	"createPullRequest":             {"pr.create", "pr.create", "repositoryId"},
	"updatePullRequest":             {"pr.edit", "pr.edit", ""},
	"closePullRequest":              {"pr.close", "pr.close", ""},
	"reopenPullRequest":             {"pr.reopen", "pr.reopen", ""},
	"deleteRef":                     {"ref.delete", "ref.delete", ""},
	"createRef":                     {"ref.create", "ref.create", ""},
	"updateRef":                     {"ref.update", "ref.update", ""},
	"updateRefs":                    {"ref.update", "ref.update.batch", ""},
	"mergeBranch":                   {"branch.merge", "branch.merge", ""},
	"createIssue":                   {"issue.create", "issue.create", "repositoryId"},
	"closeIssue":                    {"issue.close", "issue.close", ""},
	"reopenIssue":                   {"issue.reopen", "issue.reopen", ""},
	"deleteIssue":                   {"issue.delete", "issue.delete", ""},
	"addComment":                    {"comment.create", "comment.create", ""},
	"updateIssueComment":            {"comment.edit", "comment.edit", ""},
	"deleteIssueComment":            {"comment.delete", "comment.delete", ""},
	"archiveRepository":             {"repo.archive", "repo.archive", ""},
	"unarchiveRepository":           {"repo.unarchive", "repo.unarchive", ""},
	"updateRepository":              {"repo.edit", "repo.edit", ""},
	"createRepository":              {"repo.create", "repo.create", ""},
	"createBranchProtectionRule":    {"branch.protection", "branch.protection.create", ""},
	"updateBranchProtectionRule":    {"branch.protection", "branch.protection.edit", ""},
	"deleteBranchProtectionRule":    {"branch.protection", "branch.protection.delete", ""},
	"revertPullRequest":             {"pr.revert", "pr.revert", ""},
	"convertPullRequestToDraft":     {"pr.draft", "pr.draft", ""},
	"markPullRequestReadyForReview": {"pr.ready", "pr.ready", ""},
	"requestReviews":                {"pr.review", "pr.review.request", ""},
	"dismissPullRequestReview":      {"pr.review", "pr.review.dismiss", ""},
	"deletePullRequestReview":       {"pr.review", "pr.review.delete", ""},
	"updatePullRequestBranch":       {"pr.update-branch", "pr.update-branch", ""},
	"addStar":                       {"star", "star.add", ""},
	"removeStar":                    {"star", "star.remove", ""},
}

func mutationOperation(field string, input map[string]any) model.Operation {
	// Preserve all values, including nested input and secrets; redaction belongs
	// to the output layer. Only merge operations need a copy for canonical keys.
	op := model.Operation{Effect: model.EffectWrite, Parameters: input}
	schema, inSchema := schemaMutations[field]
	if !inSchema {
		op.Family = model.FamilyUnknown
		op.Action = "graphql.mutation." + field
		op.UnknownReason = model.UnknownMutation
		op.Detail = "not in schema snapshot (" + schemaSnapshotDate + ")"
		return op
	}
	idField := schema.PrimaryID
	mapping, curated := mutationRegistry[field]
	if curated {
		op.Family, op.Action = mapping.family, mapping.action
		if mapping.nodeID != "" {
			idField = mapping.nodeID
		}
		switch field {
		case "createRepository":
			// Owner/team IDs are not the repository being created.
			idField = ""
		case "submitPullRequestReview":
			if inputString(input, idField) == "" {
				idField = "pullRequestReviewId"
			}
		}
	} else {
		op.Family, op.Action = "graphql.mutation", "graphql."+field
		op.Detail = schema.Description
		if schema.Deprecated {
			op.Detail = joinText(op.Detail, "deprecated: "+schema.DeprecationReason)
		}
		if idField == "" && len(schema.IDFields) > 1 {
			var candidates strings.Builder
			candidates.WriteString("ambiguous target ID fields: ")
			for i, candidate := range schema.IDFields {
				if i > 0 {
					candidates.WriteString(", ")
				}
				candidates.WriteString("input.")
				candidates.WriteString(candidate.Name)
			}
			op.Unresolved = append(op.Unresolved, model.Unresolved{
				Field: "target.nodeId", Reason: candidates.String(), Source: "graphql",
			})
		}
	}
	if idField != "" {
		op.Target.NodeID = inputString(input, idField)
		if op.Target.NodeID == "" {
			op.Unresolved = append(op.Unresolved, model.Unresolved{
				Field: "target.nodeId", Reason: "input." + idField + " is unavailable", Source: "graphql",
			})
		}
	}
	if !curated {
		return op
	}
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
