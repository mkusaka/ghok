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
}

type wrapperDecision struct {
	Decision      string
	RelabelPolicy bool
	Denials       []denial
	ExitCode      int
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
				decision.ExitCode = 77
			}
			for _, content := range []bool{true, false} {
				for _, h := range pre.Results {
					if h.Effective != protocol.DecisionDeny || (h.Outcome == protocol.OutcomeOK) != content {
						continue
					}
					message := h.Message
					if message == "" {
						message = h.Reason
					}
					if message == "" {
						message = h.Error
					}
					d := denial{Source: "hook", ID: h.HookID, Message: message}
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
	if stdinSkipped && len(evaluation.Rules) == 0 && pre.Decision != protocol.DecisionDeny {
		decision.Decision = "allow"
		decision.RelabelPolicy = true
	}
	return decision
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
		_, _ = fmt.Fprintln(w, terminalText(entry))
	}
}
