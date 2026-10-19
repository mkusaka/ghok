//go:build unix

package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
)

type denial struct {
	Source    string
	ID        string
	Message   string
	ErrorKind string
	// Stderr is the tail of a malfunctioning hook's stderr (error denials only).
	Stderr []string
}

// attachHookStderr adds the last lines of each malfunctioning hook's stderr to
// its denial so the cause (e.g. "env: bun: No such file or directory") is
// visible without opening the log. Hooks with redactInput handle sensitive
// data, so their stderr is withheld.
func attachHookStderr(denials []denial, pre hooks.Aggregate, configured []config.Hook) {
	results := make(map[string]protocol.HookResult, len(pre.Results))
	for _, r := range pre.Results {
		results[r.HookID] = r
	}
	for i := range denials {
		d := &denials[i]
		if d.Source != "hook" || d.ErrorKind == "" {
			continue
		}
		d.Stderr = hookStderr(results[d.ID], configured)
	}
}

func hookStderr(result protocol.HookResult, configured []config.Hook) []string {
	for _, h := range configured {
		if h.ID == result.HookID && h.RedactInput {
			return []string{"(withheld; redactInput)"}
		}
	}
	return stderrTail(result.Stderr, 2, 200)
}

// stderrTail returns the last n non-empty lines, each cut to max bytes:
// interpreters print the actual failure last.
func stderrTail(s string, n, max int) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimRight(line, "\r \t"); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, line := range lines {
		if len(line) > max {
			lines[i] = line[:max] + "…"
		}
	}
	return lines
}

type wrapperDecision struct {
	Decision      string
	RelabelPolicy bool
	Denials       []denial
	ExitCode      int
}

// evaluateConfig also accounts for inactive project gates without executing them.
func evaluateConfig(c *config.Config, subject policy.Subject) policy.Evaluation {
	evaluation := policy.Evaluate(c.Policy, subject)
	subject.Phase = config.PhasePre
	for _, h := range c.IgnoredHooks {
		if !h.GateBlocked {
			continue
		}
		yes, _ := policy.Matches(h.Match, h.Unless, subject)
		if !yes {
			yes, _ = policy.MatchMaybe(h.Match, h.Unless, subject)
		}
		if !yes {
			continue
		}
		reason := fmt.Sprintf("hook %s is not trusted (changed since trust); run 'ghok config trust'", h.ID)
		evaluation.WouldDeny = true
		evaluation.Decision = protocol.DecisionDeny
		if c.Policy.Mode == config.ModeObserve {
			evaluation.Decision = protocol.DecisionAudit
		}
		evaluation.Reasons = append(evaluation.Reasons, reason)
		evaluation.DenyReasons = append(evaluation.DenyReasons, reason)
		evaluation.Denials = append(evaluation.Denials, policy.Denial{Kind: policy.DenialKind("config"), ID: h.ID, Reason: reason})
	}
	return evaluation
}

func decide(mode string, evaluation policy.Evaluation, pre hooks.Aggregate, stdinSkipped bool) wrapperDecision {
	decision := wrapperDecision{Decision: "allow"}
	wouldDeny := evaluation.WouldDeny || evaluation.Decision == protocol.DecisionDeny || pre.Decision == protocol.DecisionDeny
	if wouldDeny {
		if mode == config.ModeEnforce {
			decision.Decision = "deny"
			decision.ExitCode = 78
			for _, d := range evaluation.Denials {
				decision.Denials = append(decision.Denials, denial{Source: string(d.Kind), ID: d.ID, Message: d.Reason})
				if string(d.Kind) != "config" {
					decision.ExitCode = 77
				}
			}
			for _, content := range []bool{true, false} {
				for _, h := range pre.Results {
					if h.Effective != protocol.DecisionDeny || (h.Outcome == protocol.OutcomeOK) != content {
						continue
					}
					d := denial{Source: "hook", ID: h.HookID, Message: hookMessage(h)}
					if content {
						decision.ExitCode = 77
					} else {
						d.ErrorKind = hookErrorKind(h)
					}
					decision.Denials = append(decision.Denials, d)
				}
			}
			return decision
		}
		decision.Decision = "would-deny"
	} else if evaluation.Decision == protocol.DecisionAudit {
		decision.Decision = "audit"
	}
	if stdinSkipped && !wouldDeny && len(evaluation.Rules) == 0 && pre.Decision != protocol.DecisionDeny {
		decision.Decision = "allow"
		decision.RelabelPolicy = true
	}
	return decision
}

func hookMessage(h protocol.HookResult) string {
	if h.Message != "" {
		return h.Message
	}
	if h.Reason != "" {
		return h.Reason
	}
	return h.Error
}

func hookErrorKind(h protocol.HookResult) string {
	switch h.Outcome {
	case protocol.OutcomeTimeout:
		return "timeout"
	case protocol.OutcomeCanceled:
		return "canceled"
	case protocol.OutcomeInvalidResponse:
		return "invalid response"
	default:
		if h.Started || h.ExitCode >= 0 {
			return fmt.Sprintf("exit %d", h.ExitCode)
		}
		return "spawn failed"
	}
}

func printDenials(w io.Writer, denials []denial, redact func(string) string) {
	if len(denials) == 0 {
		return
	}
	source := func(d denial) string {
		if d.ID == "" {
			return d.Source
		}
		id := d.ID
		if d.Source != "policy" {
			id = redact(id)
		}
		id = terminalText(strings.ReplaceAll(id, "\n", `\n`))
		return d.Source + " " + id
	}
	first := denials[0]
	header := "ghok: denied by " + source(first)
	if first.ErrorKind != "" {
		header += " (error: " + first.ErrorKind + ")"
	}
	if len(denials) > 1 {
		header += fmt.Sprintf(" (+%d more)", len(denials)-1)
	}
	_, _ = fmt.Fprintln(w, header)
	for _, d := range denials {
		entry := "  - " + source(d) + ": " + strings.ReplaceAll(redact(d.Message), "\n", "\n    ")
		for _, line := range d.Stderr {
			entry += "\n      stderr: " + redact(line)
		}
		_, _ = fmt.Fprintln(w, terminalText(entry))
	}
}

// printWarning is shared by gh-runs-only diagnostics. Callers must first rule
// out an enforced denial; the entire warning uses the denial display bounds.
func printWarning(w io.Writer, source, id, detail, message string, redact func(string) string) {
	entry := "ghok: warning from " + source + " " + strings.ReplaceAll(redact(id), "\n", `\n`)
	if detail != "" {
		entry += " (" + strings.ReplaceAll(detail, "\n", `\n`) + ")"
	}
	entry += "\n  " + strings.ReplaceAll(redact(message), "\n", "\n  ")
	_, _ = fmt.Fprintln(w, terminalText(entry))
}

func resultWarnings(source string, results []protocol.HookResult) []protocol.Warning {
	var warnings []protocol.Warning
	for _, result := range results {
		text := result.Warning
		if result.ContinuedOnError {
			text = hookMessage(result)
		}
		if result.ContinuedOnError || result.Outcome == protocol.OutcomeOK && result.Decision != protocol.DecisionDeny && text != "" {
			warnings = append(warnings, protocol.Warning{Source: source, ID: result.HookID, Text: text})
		}
	}
	return warnings
}

func printWarnings(w io.Writer, warnings []protocol.Warning, redact func(string) string) {
	for _, warning := range warnings {
		printWarning(w, warning.Source, warning.ID, "", warning.Text, redact)
	}
}

func printHookWarnings(w io.Writer, pre hooks.Aggregate, configured []config.Hook, redact func(string) string) {
	for _, result := range pre.Results {
		if result.Outcome == protocol.OutcomeOK && result.Decision != protocol.DecisionDeny && result.Warning != "" {
			printWarning(w, "hook", result.HookID, "", result.Warning, redact)
		}
		if !result.ContinuedOnError {
			continue
		}
		message := hookMessage(result)
		if result.Outcome != protocol.OutcomeOK {
			for _, line := range hookStderr(result, configured) {
				message += "\n  stderr: " + line
			}
		}
		printWarning(w, "hook", result.HookID, "continue-on-error", message, redact)
	}
}
