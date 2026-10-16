package native

import (
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

// argvLocations records syntax only after pflag has accepted the complete input.
// indexes maps leaf-parser tokens back to the alias-expanded argument vector.
func argvLocations(c commandSpec, args []string, indexes []int) (map[string][]model.ArgSpan, []model.ArgSpan) {
	flags := make(map[string][]model.ArgSpan)
	var positionals []model.ArgSpan
	terminated := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !terminated && arg == "--" {
			terminated = true
			continue
		}
		if terminated || len(arg) < 2 || arg[0] != '-' {
			positionals = append(positionals, model.ArgSpan{Start: indexes[i], End: indexes[i] + 1})
			continue
		}
		start := i
		if strings.HasPrefix(arg, "--") {
			name, _, attached := strings.Cut(arg[2:], "=")
			f, ok := lookupFlag(c, name, 0)
			if !ok {
				return nil, nil
			}
			if !attached && f.noOptDefVal == "" {
				i++
				if i >= len(args) || indexes[i] != indexes[start]+1 {
					return nil, nil
				}
			}
			flags[name] = append(flags[name], model.ArgSpan{Start: indexes[start], End: indexes[i] + 1})
			continue
		}
		type shortPart struct {
			name string
			argv []string
		}
		var parts []shortPart
		for rest := arg[1:]; rest != ""; {
			f, ok := lookupFlag(c, "", rest[0])
			if !ok {
				return nil, nil
			}
			part := shortPart{name: f.long}
			switch {
			case len(rest) > 2 && rest[1] == '=':
				part.argv = []string{"-" + rest}
				rest = ""
			case f.noOptDefVal != "":
				part.argv = []string{"-" + rest[:1]}
				rest = rest[1:]
			case len(rest) > 1:
				part.argv = []string{"-" + rest}
				rest = ""
			default:
				i++
				if i >= len(args) || indexes[i] != indexes[start]+1 {
					return nil, nil
				}
				part.argv = []string{"-" + rest, args[i]}
				rest = ""
			}
			parts = append(parts, part)
		}
		for j, part := range parts {
			duplicate := false
			for _, prior := range parts[:j] {
				duplicate = duplicate || prior.name == part.name
			}
			if duplicate {
				continue
			}
			span := model.ArgSpan{Start: indexes[start], End: indexes[i] + 1}
			for _, other := range parts {
				if other.name != part.name {
					span.Keep = append(span.Keep, other.argv...)
					span.KeepGroups = append(span.KeepGroups, other.argv)
				}
			}
			flags[part.name] = append(flags[part.name], span)
		}
	}
	return flags, positionals
}

func extractEditable(op *model.Operation, c commandSpec, p parsedArgs, args []string, indexes []int) {
	if c.disableFlagParsing || op.Effect == model.EffectUnknown || len(p.errors) != 0 || len(p.unknown) != 0 {
		return
	}
	spans, positionals := argvLocations(c, args, indexes)
	if spans == nil || len(positionals) != len(p.positionals) {
		return
	}
	op.Editable = make(map[string]model.EditLocator)
	title, body, bodyFile, notes, notesFile := nativeTextFlags(c.path)
	for _, f := range c.flags {
		if !f.editable || f.long == title || f.long == body || f.long == bodyFile || f.long == notes || f.long == notesFile {
			continue
		}
		op.Editable["parameters/"+f.paramKey] = model.EditLocator{Kind: "argv", Flag: f.long, ValueType: f.valueType, Spans: spans[f.long], Parameter: f.paramKey}
	}
	addNativeTextLocator(op, c, p, spans, "title", title, "")
	addNativeTextLocator(op, c, p, spans, "body", body, bodyFile)
	addNativeTextLocator(op, c, p, spans, "notes", notes, notesFile)
	addNativeTargetLocators(op, c, p, spans, positionals)
}

func nativeTextFlags(path string) (title, body, bodyFile, notes, notesFile string) {
	switch path {
	case "pr create", "issue create", "pr edit", "issue edit":
		return "title", "body", "body-file", "", ""
	case "pr comment", "issue comment", "pr review":
		return "", "body", "body-file", "", ""
	case "pr merge":
		return "subject", "body", "body-file", "", ""
	case "pr close", "pr reopen", "issue close", "issue reopen":
		return "", "comment", "", "", ""
	case "release create", "release edit":
		return "title", "", "", "notes", "notes-file"
	}
	return "", "", "", "", ""
}

func addNativeTextLocator(op *model.Operation, c commandSpec, p parsedArgs, spans map[string][]model.ArgSpan, field, inline, file string) {
	if inline == "" || p.present("recover") || (file != "" && p.present(inline) && p.present(file)) {
		return
	}
	var text *model.Text
	if op.Texts != nil {
		switch field {
		case "title":
			text = op.Texts.Title
		case "body":
			text = op.Texts.Body
		case "notes":
			text = op.Texts.Notes
		}
	}
	flag, kind := inline, "argv"
	if text == nil {
		switch c.path {
		case "pr edit", "issue edit", "pr merge", "release create", "release edit":
		case "pr close", "pr reopen", "issue close", "issue reopen":
		case "pr review":
			if !p.enabled("approve") {
				return
			}
		default:
			return
		}
	} else {
		if text.Truncated || text.Status != "" {
			return
		}
		for _, u := range op.Unresolved {
			if u.Field == "texts."+field {
				return
			}
		}
		switch text.Source {
		case "inline":
		case "file", "stdin":
			flag, kind = file, text.Source
		default:
			return
		}
	}
	f, ok := lookupFlag(c, flag, 0)
	if !ok {
		return
	}
	locator := model.EditLocator{Kind: kind, Flag: flag, ValueType: f.valueType, Spans: spans[flag], Parameter: f.paramKey, Text: field}
	switch kind {
	case "file":
		locator.Input = text.Path
	case "stdin":
		locator.Input = "-"
	}
	op.Editable["texts/"+field+"/value"] = locator
	// Body flags are presence markers; file flags describe paths, not prose.
	if kind == "argv" && flag != "body" {
		op.Editable["parameters/"+f.paramKey] = locator
	}
}

func addNativeTargetLocators(op *model.Operation, c commandSpec, p parsedArgs, spans map[string][]model.ArgSpan, positionals []model.ArgSpan) {
	forwarded := (c.path == "repo clone" || c.path == "repo fork") && p.dashIndex >= 0
	if forwarded {
		positionals = positionals[:p.dashIndex]
	}
	selector := ""
	if len(positionals) > 0 {
		selector = p.positionals[0]
	}
	// URL selectors supply several coupled target fields, not a single argv
	// value. Do not advertise an independent repository or number replacement.
	if strings.Contains(selector, "://") {
		return
	}
	if c.positional == "pr" || c.positional == "issue" {
		if len(positionals) == 0 {
			op.Editable["target/number"] = model.EditLocator{Kind: "positional", ValueType: "string"}
		} else if len(positionals) == 1 {
			if number, err := strconv.Atoi(strings.TrimPrefix(selector, "#")); err == nil && number > 0 {
				op.Editable["target/number"] = model.EditLocator{Kind: "positional", ValueType: "string", Spans: positionals[:1]}
			}
		}
	}
	if c.positional == "repo" {
		if selector != "" && op.Target.Repository != "" {
			op.Editable["target/repository"] = model.EditLocator{Kind: "positional", ValueType: "string", Spans: positionals[:1]}
		} else if selector == "" && !forwarded {
			op.Editable["target/repository"] = model.EditLocator{Kind: "positional", ValueType: "string"}
		}
		return
	}
	if !c.repo || p.value("org") != "" || p.enabled("user") || p.present("issue-repo") {
		return
	}
	if f, ok := lookupFlag(c, "repo", 0); ok {
		op.Editable["target/repository"] = model.EditLocator{Kind: "argv", Flag: "repo", ValueType: f.valueType, Spans: spans["repo"]}
	}
}
