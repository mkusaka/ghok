// Package rest is the L2 normalizer for `gh api` REST calls.
package rest

import "github.com/mkusaka/ghok/internal/model"

// Normalize classifies a parsed non-GraphQL `gh api` request.
func Normalize(inv model.Invocation, req model.APIRequest) model.Result {
	return model.NewUnknown(model.LevelL2, model.UnknownEndpoint, "L2 normalizer not implemented") // TODO(wave1-L2)
}
