// Package graphql is the L3 normalizer for `gh api graphql` (AST based).
package graphql

import (
	"github.com/mkusaka/ghok/internal/model"
	_ "github.com/vektah/gqlparser/v2/parser"
)

// Normalize classifies a parsed GraphQL `gh api` request.
func Normalize(inv model.Invocation, req model.APIRequest) model.Result {
	return model.NewUnknown(model.LevelL3, model.UnknownMutation, "L3 normalizer not implemented") // TODO(wave1-L3)
}
