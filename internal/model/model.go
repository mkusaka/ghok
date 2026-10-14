// Package model holds the canonical, versioned data model shared by every
// ghok layer: normalizer output (Result/Operation), the invocation handed to
// normalizers, and parsed `gh api` requests.
//
// This package is a contract. Changing a field here changes the hook JSON
// protocol (schemaVersion) and every normalizer.
package model

// SchemaVersion of Result and of the hook protocol.
const SchemaVersion = 1

// Level is the normalization layer that produced a classification.
type Level string

const (
	LevelNone Level = "none" // nothing could be classified
	LevelL1   Level = "L1"   // native gh command (gh pr merge ...)
	LevelL2   Level = "L2"   // gh api REST
	LevelL3   Level = "L3"   // gh api graphql AST
)

// Confidence says how sure the normalizer is about the *identity* of the
// operations it reports.
type Confidence string

const (
	ConfidenceExact     Confidence = "exact"     // syntactically certain (AST field / typed route / known command)
	ConfidenceHeuristic Confidence = "heuristic" // inferred, may be wrong
	ConfidenceNone      Confidence = "none"      // nothing recognised
)

// Resolution says how complete targets/parameters are.
type Resolution string

const (
	ResolutionComplete   Resolution = "complete"   // every target field needed is known
	ResolutionPartial    Resolution = "partial"    // some fields unresolved (see Operation.Unresolved)
	ResolutionDeferred   Resolution = "deferred"   // resolvable later by the resolver (git/API), not yet done
	ResolutionUnresolved Resolution = "unresolved" // cannot be resolved
)

// Effect is the side-effect class of an operation.
type Effect string

const (
	EffectRead    Effect = "read"
	EffectWrite   Effect = "write"
	EffectUnknown Effect = "unknown" // never treat as safe
)

// Execution says whether an operation will actually run as part of the request.
type Execution string

const (
	ExecutionWillRun     Execution = "will-run"     // default
	ExecutionMaybe       Execution = "maybe"        // e.g. @include(if: $unresolvedVar)
	ExecutionNotSelected Execution = "not-selected" // operation not chosen by operationName; Evidence explains
	ExecutionSkipped     Execution = "skipped"      // @skip(if:true)/@include(if:false) proven; Evidence explains
)

// Result is the output of normalization for one gh invocation.
type Result struct {
	SchemaVersion  int            `json:"schemaVersion"`
	Classification Classification `json:"classification"`
	Operations     []Operation    `json:"operations"`
	// Source is the original, redaction-safe description of what was invoked.
	Source Source `json:"source"`
	// Errors are non-fatal problems (parse errors, unreadable input, ...).
	// A non-empty Errors with an unknown operation means "could not tell".
	Errors []string `json:"errors,omitempty"`
}

type Classification struct {
	Level      Level      `json:"level"`
	Confidence Confidence `json:"confidence"`
	Resolution Resolution `json:"resolution"`
	// MutationFree is true only when the normalizer *proved* that no write
	// happens (e.g. a parsed GraphQL query document, a GET REST call, a known
	// read-only command). It is false when unknown. Never set it on guesses.
	MutationFree bool `json:"mutationFree"`
}

// Operation is one semantic operation. A single invocation may yield many
// (e.g. a GraphQL document with several mutation fields).
type Operation struct {
	// Family groups operations with related meaning, e.g. "pr.merge".
	// Unknown operations use family "unknown".
	Family string `json:"family"`
	// Action is the specific action, e.g. "pr.merge.request" (gh pr merge:
	// user intent, server decides merge/auto/queue), "pr.merge.direct",
	// "pr.merge.auto.enable", "pr.merge.auto.disable", "pr.merge.queue.enqueue".
	// Hook/policy operation globs match against Action.
	Action     string         `json:"action"`
	Effect     Effect         `json:"effect"`
	Target     Target         `json:"target"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Execution  Execution      `json:"execution"`
	// Evidence explains Execution != will-run, or other proofs.
	Evidence   string       `json:"evidence,omitempty"`
	Unresolved []Unresolved `json:"unresolved,omitempty"`
	// UnknownReason is set (non-empty) iff Family == "unknown" or Effect == unknown.
	UnknownReason UnknownReason `json:"unknownReason,omitempty"`
	// Detail is free-form human-readable explanation.
	Detail string `json:"detail,omitempty"`
	// Origin ties the operation back to its source (command path, REST route
	// template, GraphQL field/alias).
	Origin Origin `json:"origin"`
}

// UnknownReason is a machine-readable reason for an unknown classification.
type UnknownReason string

const (
	UnknownNone             UnknownReason = ""
	UnknownCommand          UnknownReason = "unknown-command"     // gh subcommand not in registry
	UnknownFlag             UnknownReason = "unknown-flag"        // unrecognised flag made parsing unsafe
	UnknownExtension        UnknownReason = "extension"           // gh extension / external executable
	UnknownShellAlias       UnknownReason = "shell-alias"         // gh alias with shell expansion
	UnknownAliasLoop        UnknownReason = "alias-loop"          // alias expansion cycle / depth limit
	UnknownEndpoint         UnknownReason = "unknown-endpoint"    // REST route not in registry
	UnknownMutation         UnknownReason = "unknown-mutation"    // GraphQL mutation field not in registry
	UnknownParseError       UnknownReason = "parse-error"         // arguments / GraphQL / JSON could not be parsed
	UnknownInputUnavailable UnknownReason = "input-unavailable"   // body/query lives in a file or stdin we could not read
	UnknownInputTooLarge    UnknownReason = "input-too-large"     // spool/size limit exceeded
	UnknownOperationAmbig   UnknownReason = "operation-ambiguous" // multiple GraphQL operations, none selected
	UnknownMalformed        UnknownReason = "malformed-request"   // structurally invalid but not a parse error
)

// FamilyUnknown is the family of operations that could not be classified.
const FamilyUnknown = "unknown"

// Target is what an operation acts on. Empty string / nil means "not known";
// the field name then appears in Operation.Unresolved when it matters.
type Target struct {
	Host       string `json:"host,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Repository string `json:"repository,omitempty"` // "owner/name"
	Number     *int   `json:"number,omitempty"`     // PR / issue number
	NodeID     string `json:"nodeId,omitempty"`     // GraphQL global ID
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"baseBranch,omitempty"`
	HeadBranch string `json:"headBranch,omitempty"`
	SHA        string `json:"sha,omitempty"`
	Ref        string `json:"ref,omitempty"` // git ref, e.g. "heads/main"
	URL        string `json:"url,omitempty"`
}

// Unresolved records a target/parameter field whose value is not known.
type Unresolved struct {
	Field  string `json:"field"`            // e.g. "target.repository", "parameters.method", "variables.id"
	Reason string `json:"reason"`           // human readable
	Source string `json:"source,omitempty"` // where it would come from: "git", "api", "variable", "stdin", ...
}

// Origin links an operation to its syntactic origin.
type Origin struct {
	Command   []string `json:"command,omitempty"`   // L1: canonical command path, e.g. ["pr","merge"]
	Method    string   `json:"method,omitempty"`    // L2: HTTP method
	Route     string   `json:"route,omitempty"`     // L2: matched route template, e.g. "/repos/{owner}/{repo}/pulls/{pull_number}/merge"
	Field     string   `json:"field,omitempty"`     // L3: schema field name, e.g. "mergePullRequest"
	Alias     string   `json:"alias,omitempty"`     // L3: response alias if different from Field
	Operation string   `json:"operation,omitempty"` // L3: GraphQL operation name ("" for anonymous)
	OpType    string   `json:"opType,omitempty"`    // L3: query|mutation|subscription
}

// Source is a redaction-safe description of the original invocation.
type Source struct {
	Kind    string   `json:"kind"`              // "command" | "rest" | "graphql" | "unknown"
	Argv    []string `json:"argv,omitempty"`    // redacted argv (after alias expansion if any)
	Alias   string   `json:"alias,omitempty"`   // alias name if expanded
	Method  string   `json:"method,omitempty"`  // REST/GraphQL HTTP method
	Path    string   `json:"path,omitempty"`    // REST path without host/prefix
	Host    string   `json:"host,omitempty"`    // host explicitly given
	GraphQL string   `json:"graphql,omitempty"` // GraphQL document (may be truncated); never contains variables values
}

// NewUnknown is a helper that builds a single-unknown-operation Result.
func NewUnknown(level Level, reason UnknownReason, detail string) Result {
	return Result{
		SchemaVersion:  SchemaVersion,
		Classification: Classification{Level: level, Confidence: ConfidenceNone, Resolution: ResolutionUnresolved},
		Operations: []Operation{{
			Family:        FamilyUnknown,
			Action:        FamilyUnknown,
			Effect:        EffectUnknown,
			Execution:     ExecutionWillRun,
			UnknownReason: reason,
			Detail:        detail,
		}},
	}
}

// IntPtr returns a pointer to n.
func IntPtr(n int) *int { return &n }
