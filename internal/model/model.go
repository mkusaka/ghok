// Package model holds the canonical, versioned data model shared by every
// ghok layer: normalizer output (Result/Operation), the invocation handed to
// normalizers, and parsed `gh api` requests.
//
// This package is the hook JSON contract. Optional additive fields preserve
// schemaVersion; incompatible changes require a new protocol version.
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
	SchemaVersion  int            `json:"schemaVersion" const:"1" doc:"Normalization and hook protocol version."`
	Classification Classification `json:"classification"`
	Operations     []Operation    `json:"operations"`
	// Source is the original, redaction-safe description of what was invoked.
	Source Source `json:"source"`
	// Errors are non-fatal problems (parse errors, unreadable input, ...).
	// A non-empty Errors with an unknown operation means "could not tell".
	Errors []string `json:"errors,omitempty"`
}

type Classification struct {
	Level      Level      `json:"level" enum:"none,L1,L2,L3" doc:"Normalization layer: native command, REST, GraphQL, or none."`
	Confidence Confidence `json:"confidence" enum:"exact,heuristic,none" doc:"Certainty of operation identity, not execution success."`
	Resolution Resolution `json:"resolution" enum:"complete,partial,deferred,unresolved" doc:"Completeness of targets and parameters; inspect unresolved entries."`
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
	Effect     Effect         `json:"effect" enum:"read,write,unknown" doc:"Side-effect classification; unknown never proves safety."`
	Target     Target         `json:"target"`
	Parameters map[string]any `json:"parameters,omitempty" doc:"Action- and layer-specific JSON values; keys are documented in docs/normalization.md."`
	Texts      *Texts         `json:"texts,omitempty"`
	Execution  Execution      `json:"execution" enum:"will-run,maybe,not-selected,skipped" doc:"Requested execution state, not proof of server-side effects."`
	// Evidence explains Execution != will-run, or other proofs.
	Evidence   string       `json:"evidence,omitempty"`
	Unresolved []Unresolved `json:"unresolved,omitempty"`
	// UnknownReason is set (non-empty) iff Family == "unknown" or Effect == unknown.
	UnknownReason UnknownReason `json:"unknownReason,omitempty" enum:"unknown-command,unknown-flag,extension,shell-alias,alias-loop,unknown-endpoint,unknown-mutation,parse-error,input-unavailable,input-too-large,operation-ambiguous,malformed-request" doc:"Machine-readable reason for an unknown classification."`
	// Detail is free-form human-readable explanation.
	Detail string `json:"detail,omitempty"`
	// Origin ties the operation back to its source (command path, REST route
	// template, GraphQL field/alias).
	Origin Origin `json:"origin"`
}

// Texts contains normalized prose supplied by a command or API request.
// Nil fields mean that no text was supplied or identified for that purpose.
type Texts struct {
	Title *Text `json:"title,omitempty"`
	Body  *Text `json:"body,omitempty"`
	Notes *Text `json:"notes,omitempty"`
}

// Text describes prose available before gh runs. Bytes counts original bytes
// before redaction, including the full size of truncated input when known.
// Unavailable input has an empty Value and an Operation.Unresolved entry.
// Status explains unavailable stdin; zero Bytes then means an unknown size.
type Text struct {
	Value      string      `json:"value"`
	Source     string      `json:"source" enum:"inline,file,stdin,editor,fill,template,unknown" doc:"Text input provenance; generated/editor text is not the final submitted value."`
	Path       string      `json:"path,omitempty"`
	Status     StdinStatus `json:"status,omitempty" enum:"not-captured,tty,too-large,timeout,error" doc:"Unavailable stdin status; omitted on successful capture."`
	Truncated  bool        `json:"truncated,omitempty"`
	Bytes      int64       `json:"bytes"`
	Redactions int         `json:"redactions"` // credential substitutions in this text
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
	// Kind describes the selected resource type; "unknown" marks an unresolved
	// polymorphic subject rather than inferring its type from a node ID.
	Kind       string `json:"kind,omitempty" enum:"unknown,pullRequest,issue,repository,ref,comment,release,gist,branchProtectionRule,checkRun,checkSuite,deployment,discussion,draftIssue,enterprise,environment,ipAllowListEntry,issueType,linkedBranch,migration,milestone,organization,packageVersion,project,projectCard,projectColumn,pullRequestReview,pullRequestReviewThread,repositoryCustomProperty,repositoryRuleset,repositoryVulnerabilityAlert,sponsor,sponsorable,team,user,workflow,workflowRun" doc:"Selected resource kind, not necessarily the created result; unknown marks an unresolved polymorphic subject."`
	Host       string `json:"host,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Repository string `json:"repository,omitempty"` // "owner/name"
	Number     *int   `json:"number,omitempty" doc:"Known PR or issue number; omitted when unknown."`
	NodeID     string `json:"nodeId,omitempty"` // GraphQL global ID
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
	OpType    string   `json:"opType,omitempty" enum:"query,mutation,subscription" doc:"GraphQL operation type."`
}

// Source is a redaction-safe description of the original invocation.
type Source struct {
	Kind    string   `json:"kind" enum:"command,rest,graphql,unknown" doc:"Syntax source kind."`
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
