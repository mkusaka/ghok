package native

import "github.com/mkusaka/ghok/internal/model"

// officialExtensionStubs are gh's install-suggestion stubs
// (pkg/extensions/official.go). gh registers them after real extensions, so an
// installed extension of the same name wins.
var officialExtensionStubs = map[string]bool{"aw": true, "stack": true, "webhook": true}

// DeclaredExtension classifies `gh <name> ...` for a user-declared extension.
// gh runs built-ins before extensions and extensions before aliases, so callers
// check this before alias expansion.
func DeclaredExtension(inv model.Invocation, argv []string) (model.Result, bool) {
	if len(argv) == 0 || IsBuiltinCommand(argv[0]) && !officialExtensionStubs[argv[0]] {
		return model.Result{}, false
	}
	effect, ok := inv.Extensions[argv[0]]
	if !ok {
		return model.Result{}, false
	}
	return extensionResult(argv, argv[0], effect), true
}

// extensionResult never inspects forwarded args: the declared effect is a user
// assertion about external code, so confidence stays heuristic (never
// mutation-free). Hooks decide finer cases from invocation.argv; args are not
// copied into parameters, which the flag-aware argv redaction of logs and
// explain output does not cover.
func extensionResult(argv []string, name string, effect model.Effect) model.Result {
	if effect == "" {
		effect = model.EffectUnknown
	}
	op := model.Operation{
		Family: "extension." + name, Action: "extension." + name, Effect: effect,
		Execution: model.ExecutionWillRun, Detail: "declared gh extension; arguments are not analyzed",
		Origin: model.Origin{Command: []string{name}},
	}
	if effect == model.EffectUnknown {
		op.UnknownReason = model.UnknownExtension
	}
	// The target is unknowable, so repository/host rules and protected
	// repositories match conservatively instead of being skipped.
	unresolved(&op, "target.repository", "extension arguments are not analyzed", "argv")
	return singleResult(argv, op, model.ConfidenceHeuristic, nil)
}
