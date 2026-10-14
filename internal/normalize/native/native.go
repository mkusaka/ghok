// Package native is the pure L1 normalizer for native gh commands.
package native

import (
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/spf13/pflag"
)

type parsedArgs struct {
	flags       map[string][]string
	positionals []string
	dashIndex   int
	unknown     []string
	ambiguous   bool
	errors      []string
}

func (p parsedArgs) value(name string) string {
	v := p.flags[name]
	if len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}
func (p parsedArgs) enabled(name string) bool { b, _ := strconv.ParseBool(p.value(name)); return b }
func (p parsedArgs) present(name string) bool { _, ok := p.flags[name]; return ok }

func lookupFlag(c commandSpec, name string, short byte) (flagSpec, bool) {
	for _, f := range c.flags {
		if (name != "" && f.long == name) || (short != 0 && f.short == short) {
			return f, true
		}
	}
	if name == "help" || short == 'h' {
		return flagSpec{long: "help", short: 'h'}, true
	}
	if name == "version" {
		return flagSpec{long: "version"}, true
	}
	return flagSpec{}, false
}

// findCommand mirrors Cobra Find's flag stripping while retaining flags before
// the command, so the leaf parser sees the same interspersed argument vector.
func findCommand(argv []string) (commandSpec, []string, bool) {
	c := commandSpec{path: "", family: "help", action: "help", effect: model.EffectRead}
	remaining := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			remaining = append(remaining, argv[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			remaining = append(remaining, a)
			name, _, attached := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			var f flagSpec
			var known bool
			if strings.HasPrefix(a, "--") {
				f, known = lookupFlag(c, name, 0)
			} else {
				f, known = lookupFlag(c, "", a[1])
				attached = len(a) > 2
			}
			// Unknown flags without '=' are stripped with a value by Cobra Find.
			if !attached && ((!known) || (f.takesValue && !f.optionalValue)) && i+1 < len(argv) {
				i++
				remaining = append(remaining, argv[i])
			}
			continue
		}
		candidate := strings.TrimSpace(c.path + " " + a)
		if alias, ok := commandAliases[candidate]; ok {
			candidate = alias
		}
		next, ok := registry[candidate]
		if !ok {
			remaining = append(remaining, argv[i:]...)
			if c.path == "" || hasChildren(c.path) {
				return c, remaining, false
			}
			break
		}
		c = next
	}
	return c, remaining, true
}

func hasChildren(path string) bool { return commandGroups[path] }

func newFlagSet(c commandSpec) *pflag.FlagSet {
	fs := pflag.NewFlagSet(c.path, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, f := range c.flags {
		short := ""
		if f.short != 0 {
			short = string(f.short)
		}
		switch {
		case !f.takesValue:
			fs.BoolP(f.long, short, false, "")
		case f.repeatable:
			fs.StringArrayP(f.long, short, nil, "")
		default:
			fs.StringP(f.long, short, "", "")
		}
		if f.optionalValue {
			fs.Lookup(f.long).NoOptDefVal = "last"
		}
	}
	if fs.Lookup("help") == nil {
		fs.Bool("help", false, "")
	}
	if fs.Lookup("version") == nil {
		fs.Bool("version", false, "")
	}
	return fs
}

// discoverUnknownFlags uses pflag's errors as evidence, not a second flag
// scanner. Temporary no-value flags let discovery continue without guessing
// unknown arity; the actual parse uses the unmodified registry and allowlist.
func discoverUnknownFlags(fs *pflag.FlagSet, argv []string, p *parsedArgs) {
	for {
		err := fs.ParseAll(argv, func(*pflag.Flag, string) error { return nil })
		var unknown *pflag.NotExistError
		if !errors.As(err, &unknown) {
			return
		}
		name := unknown.GetSpecifiedName()
		short := ""
		if unknown.GetSpecifiedShortnames() != "" {
			short = unknown.GetSpecifiedShortnames()[:1]
			p.unknown = append(p.unknown, "-"+short)
			p.ambiguous = true
			// A shorthand may already be a registered long name.
			for fs.Lookup(name) != nil {
				name += "-"
			}
		} else {
			p.unknown = append(p.unknown, "--"+name)
			// A bare occurrence anywhere is conservatively ambiguous, even
			// if another occurrence is a literal value or follows '--'.
			p.ambiguous = p.ambiguous || slices.Contains(argv, "--"+name)
		}
		fs.StringP(name, short, "", "")
		fs.Lookup(name).NoOptDefVal = "unknown"
	}
}

func parseArgs(c commandSpec, argv []string) parsedArgs {
	p := parsedArgs{flags: make(map[string][]string), dashIndex: -1}
	fs := newFlagSet(c)
	err := fs.Parse(argv)
	var unknown *pflag.NotExistError
	if errors.As(err, &unknown) {
		discoverUnknownFlags(fs, argv, &p)
		fs = newFlagSet(c)
		fs.ParseErrorsAllowlist.UnknownFlags = true
		err = fs.Parse(argv)
	}
	fs.Visit(func(f *pflag.Flag) {
		if values, ok := f.Value.(pflag.SliceValue); ok {
			p.flags[f.Name] = values.GetSlice()
		} else {
			p.flags[f.Name] = []string{f.Value.String()}
		}
	})
	p.positionals = fs.Args()
	p.dashIndex = fs.ArgsLenAtDash()
	if errors.Is(err, pflag.ErrHelp) {
		// gh inherits a long-only --help; unregistered -h is pflag's
		// help sentinel, unless a command owns that shorthand.
		p.flags["help"] = []string{"true"}
	} else if err != nil {
		p.errors = append(p.errors, err.Error())
	}
	return p
}

// Normalize classifies alias-expanded argv without filesystem, git or API I/O.
func Normalize(inv model.Invocation, argv []string) model.Result {
	c, args, known := findCommand(argv)
	var p parsedArgs
	if c.path == "extension exec" {
		p = parsedArgs{positionals: args, dashIndex: -1}
	} else {
		p = parseArgs(c, args)
	}
	// Unknown commands may be extensions, and extension exec forwards even
	// --help to external code (DisableFlagParsing). Never prove those safe.
	if known && c.effect != model.EffectUnknown && len(p.unknown) == 0 && len(p.errors) == 0 && (p.enabled("help") || (len(argv) > 0 && argv[0] == "help") || p.enabled("version")) {
		return singleResult(argv, model.Operation{Family: "help", Action: "help", Effect: model.EffectRead, Execution: model.ExecutionWillRun}, model.ConfidenceExact, nil)
	}
	if !known {
		r := model.NewUnknown(model.LevelL1, model.UnknownCommand, "may be an extension or a newer gh command")
		r.Source = model.Source{Kind: "command", Argv: argv}
		return r
	}
	op := model.Operation{Family: c.family, Action: c.action, Effect: c.effect, Execution: model.ExecutionWillRun, Origin: model.Origin{Command: strings.Fields(c.path)}, Parameters: make(map[string]any)}
	if op.Effect == model.EffectUnknown {
		op.UnknownReason = model.UnknownCommand
		op.Detail = "runtime behavior is not normalized; side effects are unknown"
		if c.path == "extension exec" {
			op.UnknownReason = model.UnknownExtension
		}
	}
	extractParameters(&op, c, p)
	if c.extractor != nil {
		c.extractor(&op, c, p, inv)
	}
	confidence := model.ConfidenceExact
	if len(p.unknown) > 0 {
		confidence = model.ConfidenceHeuristic
		appendDetail(&op, "unknown flags: "+strings.Join(p.unknown, ", ")+"; gh v2.102.0 rejects these before execution; a newer gh may accept them")
		if p.ambiguous {
			unresolved(&op, "target.selector", "unknown flag arity makes positional arguments ambiguous", "argv")
		}
	}
	if len(p.errors) > 0 {
		confidence = model.ConfidenceHeuristic
		appendDetail(&op, strings.Join(p.errors, "; "))
	}
	if c.path == "" && len(p.unknown) > 0 {
		op.Family = model.FamilyUnknown
		op.Action = model.FamilyUnknown
		op.Effect = model.EffectUnknown
		op.UnknownReason = model.UnknownFlag
	}
	return singleResult(argv, op, confidence, p.errors)
}

func singleResult(argv []string, op model.Operation, confidence model.Confidence, errors []string) model.Result {
	resolution := model.ResolutionComplete
	for _, u := range op.Unresolved {
		if u.Source == "git" || u.Source == "api" {
			if resolution == model.ResolutionComplete {
				resolution = model.ResolutionDeferred
			}
		} else {
			resolution = model.ResolutionPartial
		}
	}
	if op.Effect == model.EffectUnknown {
		resolution = model.ResolutionUnresolved
	}
	return model.Result{SchemaVersion: model.SchemaVersion, Classification: model.Classification{Level: model.LevelL1, Confidence: confidence, Resolution: resolution, MutationFree: op.Effect == model.EffectRead && confidence == model.ConfidenceExact}, Operations: []model.Operation{op}, Source: model.Source{Kind: "command", Argv: argv}, Errors: errors}
}
func appendDetail(op *model.Operation, s string) {
	if op.Detail != "" {
		op.Detail += "; "
	}
	op.Detail += s
}
func unresolved(op *model.Operation, field, reason, source string) {
	for _, u := range op.Unresolved {
		if u.Field == field {
			return
		}
	}
	op.Unresolved = append(op.Unresolved, model.Unresolved{Field: field, Reason: reason, Source: source})
}
func camel(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func extractParameters(op *model.Operation, c commandSpec, p parsedArgs) {
	for _, f := range c.flags {
		if !p.present(f.long) || f.long == "repo" || f.long == "help" {
			continue
		}
		key := camel(f.long)
		switch f.long {
		case "body", "body-file", "env-file":
			op.Parameters[key] = true
			continue
		}
		if !f.takesValue {
			op.Parameters[key] = p.enabled(f.long)
		} else if f.repeatable {
			op.Parameters[key] = p.flags[f.long]
		} else {
			op.Parameters[key] = p.value(f.long)
		}
	}
	path := c.path
	if strings.HasPrefix(path, "auth ") {
		op.Parameters["sensitive"] = path == "auth token" || p.enabled("with-token") || p.enabled("show-token")
		if p.enabled("with-token") {
			op.Parameters["tokenFromStdin"] = true
		}
	}
	if path == "secret set" || path == "variable set" {
		op.Parameters["sensitive"] = true
		op.Parameters["valueFromStdin"] = !p.present("body") && !p.present("env-file")
	}
	if path == "pr merge" {
		op.Action = "pr.merge.request"
		op.Family = "pr.merge"
		switch {
		case p.enabled("auto"):
			op.Action = "pr.merge.auto.enable"
		case p.enabled("disable-auto"):
			op.Action = "pr.merge.auto.disable"
		case p.enabled("admin"):
			op.Action = "pr.merge.direct"
		}
		method := ""
		for _, m := range []string{"merge", "squash", "rebase"} {
			if p.enabled(m) {
				method = m
			}
		}
		op.Parameters["method"] = method
		op.Parameters["deleteBranch"] = p.enabled("delete-branch")
		op.Parameters["admin"] = p.enabled("admin")
		appendDetail(op, "intent only: the concrete side effect is decided by the server and runtime merge-queue/protection state")
	}
	if path == "pr review" {
		for _, v := range []string{"approve", "request-changes", "comment"} {
			if p.enabled(v) {
				op.Action = "pr.review." + v
				break
			}
		}
		if op.Action == "pr.review" {
			unresolved(op, "parameters.reviewEvent", "review event selected interactively", "interactive")
		}
	}
	if path == "repo set-default" && p.enabled("view") {
		op.Effect = model.EffectRead
		op.Action = "repo.set-default.view"
	}
	if path == "secret set" && p.enabled("no-store") {
		op.Effect = model.EffectRead
		op.Action = "secret.set.encrypt"
	}
	if path == "issue develop" && p.enabled("list") {
		op.Effect = model.EffectRead
		op.Action = "issue.develop.list"
	}
}
