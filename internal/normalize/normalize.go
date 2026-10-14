// Package normalize turns a gh invocation into a canonical model.Result by
// dispatching to the L1 (native), L2 (rest) and L3 (graphql) normalizers.
//
// Normalize is pure: all I/O is injected through model.Invocation. Results
// may contain secrets (tokens in argv/variables); redact at output boundaries
// (hook input, logs, explain) with internal/redact.
package normalize

import (
	"errors"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
	"github.com/mkusaka/ghok/internal/normalize/graphql"
	"github.com/mkusaka/ghok/internal/normalize/native"
	"github.com/mkusaka/ghok/internal/normalize/rest"
)

// Normalize classifies inv. It never panics on malformed input; anything it
// cannot understand becomes an explicit unknown operation.
func Normalize(inv model.Invocation) (res model.Result) {
	defer func() {
		if r := recover(); r != nil {
			res = model.NewUnknown(model.LevelNone, model.UnknownParseError, "internal normalizer panic")
			res.Source = model.Source{Kind: "unknown", Argv: inv.Argv}
		}
	}()

	expanded, alias, reason := native.ExpandAlias(inv.Argv, inv.Aliases)
	if reason != model.UnknownNone {
		res = model.NewUnknown(model.LevelL1, reason, "alias could not be expanded safely")
		res.Source = model.Source{Kind: "unknown", Argv: inv.Argv, Alias: alias}
		return res
	}

	if len(expanded) > 0 && expanded[0] == "api" {
		req, err := apiargs.Parse(expanded[1:])
		switch {
		case errors.Is(err, apiargs.ErrHelp):
			res = helpResult()
		case err != nil:
			res = model.NewUnknown(model.LevelL2, model.UnknownParseError, err.Error())
			res.Source = model.Source{Kind: "unknown"}
		case apiargs.IsGraphQL(req):
			res = graphql.Normalize(inv, req)
		default:
			res = rest.Normalize(inv, req)
		}
	} else {
		res = native.Normalize(inv, expanded)
	}
	res.SchemaVersion = model.SchemaVersion
	res.Source.Argv = expanded
	res.Source.Alias = alias
	return res
}

func helpResult() model.Result {
	return model.Result{
		Classification: model.Classification{Level: model.LevelL2, Confidence: model.ConfidenceExact, Resolution: model.ResolutionComplete, MutationFree: true},
		Operations:     []model.Operation{{Family: "help", Action: "help", Effect: model.EffectRead, Execution: model.ExecutionWillRun}},
		Source:         model.Source{Kind: "command"},
	}
}
