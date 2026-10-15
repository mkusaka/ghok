// Package logging writes credential-redacted, content-minimized records as JSONL.
package logging

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
)

// Record describes one invocation. Write redacts a copy; callers retain their
// original data. Exec output bodies are never serialized, even when supplied.
type Record struct {
	Time           time.Time             `json:"time"`
	InvocationID   string                `json:"invocationId"`
	Mode           string                `json:"mode"`
	DurationMs     int64                 `json:"durationMs"`
	Argv           []string              `json:"argv"`
	Alias          string                `json:"alias,omitempty"`
	Classification model.Classification  `json:"classification"`
	Operations     []model.Operation     `json:"operations"`
	Source         *model.Source         `json:"source,omitempty"`
	Errors         []string              `json:"errors,omitempty"`
	Policy         *protocol.PolicyInfo  `json:"policy,omitempty"`
	Hooks          []protocol.HookResult `json:"hooks,omitempty"`
	Decision       string                `json:"decision"`
	Exec           *protocol.ExecResult  `json:"exec,omitempty"`
	Diagnostics    []string              `json:"diagnostics,omitempty"`
}

// NewInvocationID returns a cryptographically random 128-bit lowercase hex ID.
func NewInvocationID() string {
	var id [16]byte
	// Go's crypto/rand.Read always fills the buffer, or terminates the process
	// if the operating system's secure entropy source irrecoverably fails.
	rand.Read(id[:])
	return hex.EncodeToString(id[:])
}

func redactRecord(r *redact.Redactor, record Record) Record {
	res := r.Result(model.Result{
		Classification: record.Classification,
		Operations:     record.Operations,
		Errors:         record.Errors,
	})
	record.Classification = res.Classification
	record.Errors = res.Errors
	for i := range record.Operations {
		if record.Operations[i].Parameters != nil {
			res.Operations[i].Parameters = minimizeValue(r, record.Operations[i].Parameters, res.Operations[i].Parameters, "").(map[string]any)
		}
		minimizeTexts(record.Operations[i].Texts, res.Operations[i].Texts)
	}
	record.Operations = res.Operations
	record.InvocationID = r.String(record.InvocationID)
	record.Mode = r.String(record.Mode)
	record.Argv = minimizeArgv(r, record.Argv)
	record.Alias = r.String(record.Alias)
	record.Decision = r.String(record.Decision)
	record.Diagnostics = redactStrings(r, record.Diagnostics)
	if record.Source != nil {
		source := *record.Source
		source.Kind = r.String(minimizeString(source.Kind, "kind"))
		source.Argv = minimizeArgv(r, source.Argv)
		source.Alias = r.String(minimizeString(source.Alias, "alias"))
		source.Method = r.String(minimizeString(source.Method, "method"))
		source.Path = r.String(source.Path)
		source.Host = r.String(source.Host)
		if source.GraphQL != "" {
			source.GraphQL = r.String(graphQLSummary(source.GraphQL))
		}
		record.Source = &source
	}
	if record.Policy != nil {
		policy := *record.Policy
		policy.Mode = r.String(policy.Mode)
		policy.Decision = protocol.Decision(r.String(string(policy.Decision)))
		policy.Reasons = redactStrings(r, policy.Reasons)
		policy.Rules = redactStrings(r, policy.Rules)
		record.Policy = &policy
	}
	if record.Hooks != nil {
		hooks := make([]protocol.HookResult, len(record.Hooks))
		for i, hook := range record.Hooks {
			hook.HookID = r.String(hook.HookID)
			hook.Phase = r.String(hook.Phase)
			hook.Outcome = protocol.Outcome(r.String(string(hook.Outcome)))
			hook.Decision = protocol.Decision(r.String(string(hook.Decision)))
			hook.Reason = r.String(hook.Reason)
			hook.Message = r.String(hook.Message)
			hook.Effective = protocol.Decision(r.String(string(hook.Effective)))
			hook.Error = r.String(hook.Error)
			hook.Stderr = r.String(hook.Stderr)
			hooks[i] = hook
		}
		record.Hooks = hooks
	}
	if record.Exec != nil {
		exec := *record.Exec
		exec.Signal = r.String(exec.Signal)
		exec.StartError = r.String(exec.StartError)
		exec.Stdout = nil
		exec.Stderr = nil
		record.Exec = &exec
	}
	return record
}

func redactStrings(r *redact.Redactor, values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = r.String(value)
	}
	return out
}

func significant(record Record) bool {
	if record.Decision != "allow" && record.Decision != "passthrough" {
		return true
	}
	if len(record.Errors) != 0 {
		return true
	}
	if record.Exec != nil && record.Exec.StartError != "" {
		return true
	}
	for _, hook := range record.Hooks {
		if hook.Error != "" || hook.ExitCode != 0 ||
			(hook.Outcome != "" && hook.Outcome != protocol.OutcomeOK && hook.Outcome != protocol.OutcomeSkipped) {
			return true
		}
	}
	return false
}
