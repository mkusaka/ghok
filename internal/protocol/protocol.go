// Package protocol defines the versioned JSON hook protocol (stdin → hook,
// hook → stdout) and the outcome model for hook executions.
package protocol

import "github.com/mkusaka/ghok/internal/model"

// Event names.
const (
	EventPre  = "pre"
	EventPost = "post"
)

// Input is written as one JSON document to a hook's stdin, then stdin is closed.
// Input is raw unless the current hook opts into redactInput.
type Input struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	Event          string                 `json:"event"` // pre|post
	EventID        string                 `json:"eventId"`
	HookID         string                 `json:"hookId"`
	Redactions     int                    `json:"redactions"`
	Invocation     InvocationInfo         `json:"invocation"`
	Classification model.Classification   `json:"classification"`
	Operations     []model.Operation      `json:"operations"`
	Errors         []string               `json:"errors,omitempty"`
	Context        *model.ResolvedContext `json:"context,omitempty"`
	Policy         *PolicyInfo            `json:"policy,omitempty"`
	Result         *ExecResult            `json:"result,omitempty"` // post only
	// Previous holds results of hooks this hook depends on (dependsOn).
	Previous []HookResult `json:"previous,omitempty"`
}

type InvocationInfo struct {
	Argv     []string `json:"argv"` // original os.Args[1:]
	Alias    string   `json:"alias,omitempty"`
	Expanded []string `json:"expanded,omitempty"` // alias-expanded analysis argv
	Cwd      string   `json:"cwd"`
	IsTTY    bool     `json:"isTTY"` // stdout is a terminal
	StdinTTY bool     `json:"stdinTTY"`
	RealGh   string   `json:"realGh,omitempty"`
	// TokenKind is the kind of env token gh will use: app|user|unknown|none
	// (see config match.token). The token itself is never included.
	TokenKind string `json:"tokenKind,omitempty"`
}

type PolicyInfo struct {
	Mode     string   `json:"mode"`
	Decision Decision `json:"decision"`
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

// Response is read from a hook's stdout. Empty stdout with exit 0 = abstain.
// Non-zero exit is an error Outcome regardless of stdout.
type Response struct {
	SchemaVersion int      `json:"schemaVersion,omitempty"`
	Decision      Decision `json:"decision"`
	Reason        string   `json:"reason,omitempty"`
	// Message is shown to the user on stderr when the final decision is deny.
	Message string `json:"message,omitempty"`
}

// Outcome is how a hook execution went, independent of its Decision.
type Outcome string

const (
	OutcomeOK              Outcome = "ok"
	OutcomeError           Outcome = "error" // non-zero exit, spawn failure
	OutcomeTimeout         Outcome = "timeout"
	OutcomeCanceled        Outcome = "canceled"         // context canceled (e.g. earlier deny, wrapper signal)
	OutcomeInvalidResponse Outcome = "invalid-response" // unparsable JSON / unknown decision
	OutcomeSkipped         Outcome = "skipped"          // optional + missing, disabled, dependency failed
)

// HookResult is the diagnostic record of one hook execution.
type HookResult struct {
	HookID   string   `json:"hookId"`
	Phase    string   `json:"phase"`
	Outcome  Outcome  `json:"outcome"`
	Decision Decision `json:"decision,omitempty"` // valid when Outcome == ok
	Reason   string   `json:"reason,omitempty"`
	Message  string   `json:"message,omitempty"`
	// Effective is the decision after applying onError (what aggregation used).
	Effective  Decision `json:"effective,omitempty"`
	ExitCode   int      `json:"exitCode"`
	DurationMs int64    `json:"durationMs"`
	Error      string   `json:"error,omitempty"`
	Stderr     string   `json:"stderr,omitempty"` // truncated, redacted
}
