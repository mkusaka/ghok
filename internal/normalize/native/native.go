// Package native is the L1 normalizer for native gh commands.
package native

import "github.com/mkusaka/ghok/internal/model"

// ExpandAlias expands a gh alias at argv[0] (plain aliases only, with gh's
// $1..$N substitution semantics). Shell aliases ("!..."), cycles and depth
// overflow yield a non-empty UnknownReason. If argv[0] is not an alias,
// argv is returned unchanged with alias "".
func ExpandAlias(argv []string, aliases map[string]string) (expanded []string, alias string, reason model.UnknownReason) {
	return argv, "", model.UnknownNone // TODO(wave1-L1)
}

// Normalize classifies a (alias-expanded) native gh command. argv excludes
// the executable name.
func Normalize(inv model.Invocation, argv []string) model.Result {
	return model.NewUnknown(model.LevelL1, model.UnknownCommand, "L1 normalizer not implemented") // TODO(wave1-L1)
}
