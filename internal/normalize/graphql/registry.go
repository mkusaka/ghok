package graphql

import (
	"path"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/actions"
)

//go:generate go run ./generate -date 2026-10-15 -sha256 3ed095654b22b1a8e7a88af36af2b28cb6d9613da332e9c57303e30476958d42 -output schema_mutations.go

// Curated identities override the generated defaults. nodeID is only needed
// when the curated target differs from the schema's primary ID selection.
type mutationMapping struct {
	actions.Spec
	nodeID string
}

var mutationRegistry = map[string]mutationMapping{
	"mergePullRequest":              {actions.Must("pr.merge.direct"), ""},
	"enablePullRequestAutoMerge":    {actions.Must("pr.merge.auto.enable"), ""},
	"disablePullRequestAutoMerge":   {actions.Must("pr.merge.auto.disable"), ""},
	"enqueuePullRequest":            {actions.Must("pr.merge.queue.enqueue"), ""},
	"dequeuePullRequest":            {actions.Must("pr.merge.queue.dequeue"), ""},
	"addPullRequestReview":          {actions.Must("pr.review"), ""},
	"submitPullRequestReview":       {actions.Must("pr.review"), "pullRequestId"},
	"createPullRequest":             {actions.Must("pr.create"), "repositoryId"},
	"updatePullRequest":             {actions.Must("pr.edit"), ""},
	"closePullRequest":              {actions.Must("pr.close"), ""},
	"reopenPullRequest":             {actions.Must("pr.reopen"), ""},
	"deleteRef":                     {actions.Must("ref.delete"), ""},
	"createRef":                     {actions.Must("ref.create"), ""},
	"updateRef":                     {actions.Must("ref.update"), ""},
	"updateRefs":                    {actions.Must("ref.update.batch"), ""},
	"mergeBranch":                   {actions.Must("branch.merge"), ""},
	"createIssue":                   {actions.Must("issue.create"), "repositoryId"},
	"closeIssue":                    {actions.Must("issue.close"), ""},
	"reopenIssue":                   {actions.Must("issue.reopen"), ""},
	"deleteIssue":                   {actions.Must("issue.delete"), ""},
	"updateIssue":                   {actions.Must("issue.edit"), "id"},
	"addComment":                    {actions.Must("comment.create"), ""},
	"updateIssueComment":            {actions.Must("comment.edit"), ""},
	"deleteIssueComment":            {actions.Must("comment.delete"), ""},
	"addReaction":                   {actions.Must("reaction.add"), ""},
	"removeReaction":                {actions.Must("reaction.remove"), ""},
	"lockLockable":                  {actions.Must("lockable.lock"), ""},
	"unlockLockable":                {actions.Must("lockable.unlock"), ""},
	"minimizeComment":               {actions.Must("comment.minimize"), ""},
	"unminimizeComment":             {actions.Must("comment.unminimize"), ""},
	"archiveRepository":             {actions.Must("repo.archive"), ""},
	"unarchiveRepository":           {actions.Must("repo.unarchive"), ""},
	"updateRepository":              {actions.Must("repo.edit"), ""},
	"createRepository":              {actions.Must("repo.create"), ""},
	"createBranchProtectionRule":    {actions.Must("branch.protection.create"), ""},
	"updateBranchProtectionRule":    {actions.Must("branch.protection.edit"), ""},
	"deleteBranchProtectionRule":    {actions.Must("branch.protection.delete"), ""},
	"revertPullRequest":             {actions.Must("pr.revert"), ""},
	"convertPullRequestToDraft":     {actions.Must("pr.draft"), ""},
	"markPullRequestReadyForReview": {actions.Must("pr.ready"), ""},
	"requestReviews":                {actions.Must("pr.review.request"), ""},
	"dismissPullRequestReview":      {actions.Must("pr.review.dismiss"), ""},
	"deletePullRequestReview":       {actions.Must("pr.review.delete"), ""},
	"updatePullRequestBranch":       {actions.Must("pr.update-branch"), ""},
	"addStar":                       {actions.Must("star.add"), ""},
	"removeStar":                    {actions.Must("star.remove"), ""},
}

// ActionRoutes returns mutation fields grouped by their curated base identity.
// Input-dependent refinements and subject specialization are not included.
func ActionRoutes() map[string][]string {
	routes := make(map[string][]string)
	for field, mapping := range mutationRegistry {
		routes[mapping.Action] = append(routes[mapping.Action], field)
	}
	return routes
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
		op.Family, op.Action, op.Effect = mapping.Family, mapping.Action, mapping.Effect
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
	setTargetKind(&op, schema, idField, field)
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
		canonicalStateAction(&op, input, "pr")
	case "updateIssue":
		canonicalStateAction(&op, input, "issue")
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
		actions.Apply(op, "pr.review.pending")
		return
	}
	switch event {
	case "APPROVE":
		actions.Apply(op, "pr.review.approve")
	case "REQUEST_CHANGES":
		actions.Apply(op, "pr.review.request-changes")
	case "COMMENT":
		actions.Apply(op, "pr.review.comment")
	default:
		op.Unresolved = append(op.Unresolved, model.Unresolved{
			Field: "parameters.event", Reason: "review event is unavailable or not recognized", Source: "graphql",
		})
	}
}

// The schema describes ID referents, including interfaces. Interface IDs are
// never decoded or guessed from their opaque value; later __typename resolution
// may specialize the action but must never generalize an already known action.
func setTargetKind(op *model.Operation, schema mutationSchema, idField, mutation string) {
	switch mutation {
	case "updateIssue":
		op.Target.Kind = "issue"
		return
	case "updateIssueComment", "deleteIssueComment":
		op.Target.Kind = "comment"
		return
	case "dequeuePullRequest":
		op.Target.Kind = "pullRequest"
		return
	case "createRepository":
		op.Target.Kind = "repository"
		return
	}
	for _, field := range schema.IDFields {
		if field.Name != idField {
			continue
		}
		switch field.Kind {
		case "", "Actor", "Assignable", "Closable", "Comment", "Labelable",
			"Lockable", "Minimizable", "Node", "Reactable", "Starrable", "Subscribable",
			"Updatable", "Votable":
			op.Target.Kind = "unknown"
			op.Unresolved = append(op.Unresolved, model.Unresolved{
				Field: "target.kind", Reason: "subject type requires __typename resolution", Source: "api",
			})
		case "IssueComment", "PullRequestReviewComment", "DiscussionComment":
			op.Target.Kind = "comment"
		default:
			op.Target.Kind = strings.ToLower(field.Kind[:1]) + field.Kind[1:]
		}
		return
	}
}

func canonicalStateAction(op *model.Operation, input map[string]any, prefix string) {
	switch input["state"] {
	case "CLOSED":
		actions.Apply(op, prefix+".close")
	case "OPEN":
		actions.Apply(op, prefix+".reopen")
	}
}

// KnownActionPattern checks generated schema-backed action names. Curated
// fields have already been checked against the shared catalogue by the caller.
func KnownActionPattern(glob string) bool {
	for _, action := range [...]string{"graphql.query", "graphql.subscription"} {
		if match, _ := path.Match(glob, action); match {
			return true
		}
	}
	for field := range schemaMutations {
		if _, curated := mutationRegistry[field]; curated {
			continue
		}
		if match, _ := path.Match(glob, "graphql."+field); match {
			return true
		}
	}
	return false
}
