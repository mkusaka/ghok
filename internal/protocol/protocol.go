// Package protocol defines the versioned JSON hook protocol (stdin → hook,
// hook → stdout) and the outcome model for hook executions.
package protocol

import "github.com/mkusaka/ghok/internal/model"

// Event names.
const (
	EventPre       = "pre"
	EventPost      = "post"
	EventTransform = "transform"
)

// Input is written as one JSON document to a hook's stdin, then stdin is closed.
// Input is raw unless the current hook opts into redactInput.
type Input struct {
	SchemaVersion  int                    `json:"schemaVersion" const:"1" doc:"Hook protocol version; always 1."`
	Event          string                 `json:"event" enum:"pre,post,transform" doc:"Hook event or request transform."`
	Phase          string                 `json:"phase,omitempty" enum:"pre" doc:"Transform phase; present for transform events."`
	EventID        string                 `json:"eventId" doc:"Invocation correlation ID, shared across hooks and phases."`
	HookID         string                 `json:"hookId" doc:"Current configured hook ID."`
	Redactions     int                    `json:"redactions" doc:"Credential substitutions in this hook's input; zero for raw input."`
	Invocation     InvocationInfo         `json:"invocation"`
	Classification model.Classification   `json:"classification"`
	Operations     []model.Operation      `json:"operations,omitempty" doc:"Normalized operations; absent means empty."`
	Errors         []string               `json:"errors,omitempty"`
	Context        *model.ResolvedContext `json:"context,omitempty"`
	Policy         *PolicyInfo            `json:"policy,omitempty"`
	Result         *ExecResult            `json:"result,omitempty"` // post only
	// Previous holds results of hooks this hook depends on (dependsOn).
	Previous []HookResult `json:"previous,omitempty"`
}

type InvocationInfo struct {
	Via      string   `json:"via" enum:"shim,exec" doc:"Wrapper entry point: shim or explicit ghok exec; both enforce the same policy."`
	Argv     []string `json:"argv,omitempty" doc:"Original arguments after argv[0]; absent means empty."`
	Alias    string   `json:"alias,omitempty"`
	Expanded []string `json:"expanded,omitempty"` // alias-expanded analysis argv
	Cwd      string   `json:"cwd"`
	IsTTY    bool     `json:"isTTY"` // stdout is a terminal
	StdinTTY bool     `json:"stdinTTY"`
	RealGh   string   `json:"realGh,omitempty"`
	// TokenKind is the kind of env token gh will use: app|user|unknown|none
	// (see config match.token). The token itself is never included.
	TokenKind  string   `json:"tokenKind,omitempty" enum:"app,user,unknown,none" doc:"Credential kind from the environment token prefix; never the token itself."`
	EnvChanged []string `json:"envChanged,omitempty" doc:"Sorted names of environment variables changed by request transforms; never their values."`
}

type PolicyInfo struct {
	Mode     string   `json:"mode" enum:"observe,enforce" doc:"Policy enforcement mode."`
	Decision Decision `json:"decision" enum:"allow,deny,abstain,audit" doc:"Policy decision; audit is not a hook response decision."`
	Reasons  []string `json:"reasons,omitempty"`
	Rules    []string `json:"rules,omitempty"` // matched rule IDs
}

// ExecResult describes how the real gh finished (post hooks only).
type ExecResult struct {
	ExitCode   int     `json:"exitCode"`         // -1 when signaled
	Signal     string  `json:"signal,omitempty"` // e.g. "SIGINT" when terminated by a signal
	CoreDump   bool    `json:"coreDump,omitempty"`
	DurationMs int64   `json:"durationMs"`
	Stdout     *Output `json:"stdout,omitempty"` // only when captureOutput
	Stderr     *Output `json:"stderr,omitempty"`
	StartError string  `json:"startError,omitempty"` // gh could not be started
}

type Output struct {
	Base64    string `json:"base64"` // raw bytes, base64 (binary-safe)
	Size      int64  `json:"size"`   // total bytes produced
	Truncated bool   `json:"truncated"`
}

// Decision is what a hook (or the policy engine) decided.
type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
	DecisionAbstain Decision = "abstain"
	DecisionAudit   Decision = "audit" // policy engine only: allow + flag
)

// Warning annotates an allowed request without changing its decision.
type Warning struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

// Response is read from a hook's stdout. Empty stdout with exit 0 = abstain.
// Non-zero exit is an error Outcome regardless of stdout.
type Response struct {
	SchemaVersion int      `json:"schemaVersion,omitempty" const:"1" doc:"Hook protocol version; omit or use 1."`
	Decision      Decision `json:"decision" enum:"allow,deny,abstain" doc:"Hook decision; allow cannot override another hook's denial."`
	Reason        string   `json:"reason,omitempty"`
	Warning       string   `json:"warning,omitempty"`
	// Message is shown to the user on stderr when the final decision is deny.
	Message string `json:"message,omitempty"`
}

// TransformResponse atomically edits the normalized request.
type TransformResponse struct {
	SchemaVersion int                `json:"schemaVersion" const:"1"`
	Set           map[string]any     `json:"set,omitempty"`
	Unset         []string           `json:"unset,omitempty"`
	Env           map[string]*string `json:"env,omitempty" doc:"Environment edits; string values set variables and null unsets them."`
	Warning       string             `json:"warning,omitempty"`
}

// Outcome is how a hook execution went, independent of its Decision.
type Outcome string

const (
	OutcomeOK              Outcome = "ok"
	OutcomeError           Outcome = "error" // non-zero exit, spawn failure
	OutcomeTimeout         Outcome = "timeout"
	OutcomeCanceled        Outcome = "canceled"         // context canceled (e.g. wrapper signal)
	OutcomeInvalidResponse Outcome = "invalid-response" // unparsable JSON / unknown decision
	OutcomeSkipped         Outcome = "skipped"          // optional + missing, disabled, dependency failed
	OutcomeUnsupportedEdit Outcome = "unsupported-edit"
)

// HookResult is the diagnostic record of one hook execution.
type HookResult struct {
	HookID   string   `json:"hookId"`
	Phase    string   `json:"phase" enum:"pre,post" doc:"Hook execution phase."`
	Outcome  Outcome  `json:"outcome" enum:"ok,error,timeout,canceled,invalid-response,unsupported-edit,skipped" doc:"Execution outcome, independent of the decision."`
	Decision Decision `json:"decision,omitempty" enum:"allow,deny,abstain" doc:"Response decision, meaningful when outcome is ok."`
	Reason   string   `json:"reason,omitempty"`
	Message  string   `json:"message,omitempty"`
	Warning  string   `json:"warning,omitempty"`
	// Effective is the decision after phase, onError and continueOnError handling.
	Effective        Decision `json:"effective,omitempty" enum:"allow,deny,abstain" doc:"Decision used after phase, onError and continueOnError handling."`
	ContinuedOnError bool     `json:"continuedOnError,omitempty" doc:"A pre-hook deny was made non-blocking by continueOnError; the original diagnostic result is preserved."`
	ExitCode         int      `json:"exitCode"`
	DurationMs       int64    `json:"durationMs"`
	Error            string   `json:"error,omitempty"`
	Stderr           string   `json:"stderr,omitempty"` // truncated, redacted
	// Started distinguishes launch failure from a signaled hook (both exit -1).
	Started bool `json:"-"`
}
