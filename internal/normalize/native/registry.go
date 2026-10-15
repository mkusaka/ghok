package native

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/actions"
)

//go:generate sh -c "cd gen && go run ."

type flagSpec struct {
	long                string
	short               byte
	valueType           string
	noOptDefVal         string
	hidden              bool
	deprecated          string
	shorthandDeprecated string
}

func (f flagSpec) repeatable() bool {
	return f.valueType == "stringArray" || f.valueType == "stringSlice"
}

type commandMetadata struct {
	canonicalPath      string
	flags              []flagSpec
	hidden             bool
	deprecated         string
	disableFlagParsing bool
	argsHint           string
	annotations        map[string]string
}

type semanticSpec struct {
	actions.Spec
	repo       bool
	positional string
	extractor  func(*model.Operation, commandSpec, parsedArgs, model.Invocation)
}

type commandSpec struct {
	path string
	commandMetadata
	semanticSpec
}

// Join once, not per invocation. Uncurated commands have an explicit unknown
// intent, even when they are a group or are invoked with help/version flags.
var registry = buildRegistry()

func buildRegistry() map[string]commandSpec {
	commands := make(map[string]commandSpec, len(curatedCommands))
	for path, metadata := range generatedCommands {
		if path != metadata.canonicalPath {
			continue
		}
		semantics, ok := curatedCommands[path]
		if !ok {
			semantics = semanticSpec{Spec: actions.Must("unknown")}
		}
		commands[path] = commandSpec{path: path, commandMetadata: metadata, semanticSpec: semantics}
	}
	return commands
}

// IsBuiltinCommand reports whether a root command or built-in alias exists.
// Wrappers use it to avoid loading gh's alias configuration for built-ins.
func IsBuiltinCommand(name string) bool {
	if name == "" {
		return false
	}
	if _, ok := registry[name]; ok {
		return true
	}
	_, ok := commandAliases[name]
	return ok
}

// Command discovery does not scan the registry per invocation.
var commandGroups = func() map[string]bool {
	groups := make(map[string]bool)
	for path := range registry {
		if i := strings.LastIndexByte(path, ' '); i >= 0 {
			groups[path[:i]] = true
		}
	}
	return groups
}()
