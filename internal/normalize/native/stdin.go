package native

import "github.com/mkusaka/ghok/internal/model"

// NeedsStdin reports whether the parsed command's body or notes comes from
// stdin. It shares text extraction's inline and interactive precedence rules.
func NeedsStdin(argv []string) (bool, string) {
	c, args, known := findCommand(argv)
	if !known || c.Family == model.FamilyUnknown || c.disableFlagParsing {
		return false, ""
	}
	p := parseArgs(c, args)
	if len(p.errors) > 0 || len(p.unknown) > 0 || p.enabled("help") || p.enabled("version") {
		return false, ""
	}
	var op model.Operation
	// Empty captured input is enough to identify provenance without adding an
	// unresolved entry for bytes that this predicate does not need to inspect.
	extractTexts(&op, c, p, model.Invocation{Stdin: model.StdinData{Status: model.StdinCaptured}})
	if op.Texts != nil {
		if op.Texts.Body != nil && op.Texts.Body.Source == "stdin" {
			return true, "body"
		}
		if op.Texts.Notes != nil && op.Texts.Notes.Source == "stdin" {
			return true, "notes"
		}
	}
	return false, ""
}
