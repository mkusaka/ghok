package normalize

import (
	"github.com/mkusaka/ghok/internal/normalize/actions"
	"github.com/mkusaka/ghok/internal/normalize/graphql"
	"github.com/mkusaka/ghok/internal/normalize/rest"
)

var specializations = map[[2]string]string{
	{"comment.create", "pullRequest"}:  "pr.comment.create",
	{"comment.create", "issue"}:        "issue.comment.create",
	{"comment.edit", "pullRequest"}:    "pr.comment.edit",
	{"comment.edit", "issue"}:          "issue.comment.edit",
	{"comment.delete", "pullRequest"}:  "pr.comment.delete",
	{"comment.delete", "issue"}:        "issue.comment.delete",
	{"lockable.lock", "pullRequest"}:   "pr.lock",
	{"lockable.lock", "issue"}:         "issue.lock",
	{"lockable.unlock", "pullRequest"}: "pr.unlock",
	{"lockable.unlock", "issue"}:       "issue.unlock",
}

// Specialize refines a generic action using a resolved subject kind. Unknown or
// unrelated kinds leave the action unchanged; specialized actions never generalize.
// The resolver does not apply this table yet: opaque node IDs reveal no type.
func Specialize(action, kind string) string {
	if specialized, ok := specializations[[2]string{action, kind}]; ok {
		return specialized
	}
	return action
}

// KnownActionPattern reports whether glob can match a curated action or an
// emitted schema/OpenAPI-backed default. Invalid globs and obsolete exact names
// match nothing. Generated rest.* and graphql.* actions remain outside the
// curated catalogue in internal/normalize/actions/actions.go.
func KnownActionPattern(glob string) bool {
	return actions.KnownPattern(glob) || rest.KnownActionPattern(glob) || graphql.KnownActionPattern(glob)
}
