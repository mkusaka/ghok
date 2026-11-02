package native

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

// officialExtensionStubs are gh's install-suggestion stubs
// (pkg/extensions/official.go). gh registers them after real extensions, so an
// installed extension of the same name wins.
var officialExtensionStubs = map[string]bool{"aw": true, "stack": true, "webhook": true}

// ExtensionAliases drops aliases named like declared extensions: gh does not
// register an alias that collides with an installed extension, so expansion
// stops there and DeclaredExtension decides.
func ExtensionAliases(inv model.Invocation) map[string]string {
	if len(inv.Extensions) == 0 {
		return inv.Aliases
	}
	aliases := make(map[string]string, len(inv.Aliases))
	for name, expansion := range inv.Aliases {
		if _, declared := inv.Extensions[name]; !declared {
			aliases[name] = expansion
		}
	}
	return aliases
}

// DeclaredExtension classifies alias-expanded argv whose command is a declared
// extension. Like Cobra Find, gh skips root flags to locate the command;
// built-ins win, then installed extensions, then aliases and official stubs.
func DeclaredExtension(inv model.Invocation, argv []string) (model.Result, bool) {
	i := rootCommandIndex(argv)
	if i < 0 {
		return model.Result{}, false
	}
	name := argv[i]
	effect, ok := inv.Extensions[name]
	if !ok || builtinShadows(name) {
		return model.Result{}, false
	}
	if _, alias := inv.Aliases[name]; alias {
		// gh runs the extension only while it is installed, which
		// normalization cannot observe; otherwise the alias runs.
		r := model.NewUnknown(model.LevelL1, model.UnknownExtension, "declared extension "+name+" is also a gh alias; installation decides which runs")
		r.Source = model.Source{Kind: "command", Argv: argv}
		return r, true
	}
	return extensionResult(argv, name, effect), true
}

// builtinShadows reports whether gh's own command wins over an extension.
// Official stubs and config-default aliases (co) are registered after
// extensions.
func builtinShadows(name string) bool {
	if officialExtensionStubs[name] || generatedCommands[name].annotations["gh:alias-expansion"] != "" {
		return false
	}
	return IsBuiltinCommand(name)
}

// rootCommandIndex mirrors findCommand's root-level Cobra flag stripping and
// returns the index of the first command word, or -1.
func rootCommandIndex(argv []string) int {
	root := registry[""]
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return -1
		}
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			return i
		}
		name, _, attached := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		var f flagSpec
		var known bool
		if strings.HasPrefix(a, "--") {
			f, known = lookupFlag(root, name, 0)
		} else {
			f, known = lookupFlag(root, "", a[1])
			attached = len(a) > 2
		}
		if !attached && (!known || f.noOptDefVal == "") {
			i++
		}
	}
	return -1
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
	unresolved(&op, "target.host", "extension arguments are not analyzed", "argv")
	return singleResult(argv, op, model.ConfidenceHeuristic, nil)
}
