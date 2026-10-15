//go:build unix

package cli

import (
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
)

type wrapperDecision struct {
	Decision      string
	Reason        string
	Prefix        string
	RelabelPolicy bool
}

func decide(mode string, evaluation policy.Evaluation, pre hooks.Aggregate, stdinSkipped bool) wrapperDecision {
	decision := wrapperDecision{Decision: "allow"}
	wouldDeny := evaluation.WouldDeny || evaluation.Decision == protocol.DecisionDeny || pre.Decision == protocol.DecisionDeny
	if wouldDeny {
		if mode == config.ModeEnforce {
			decision.Decision = "deny"
			decision.Reason = strings.Join(evaluation.DenyReasons, "; ")
			if pre.Decision == protocol.DecisionDeny {
				hookReason := ""
				for _, h := range pre.Results {
					if h.Effective == protocol.DecisionDeny {
						hookReason = h.Reason
						if hookReason == "" {
							hookReason = h.Error
						}
						break
					}
				}
				if hookReason != "" {
					if decision.Reason != "" {
						decision.Reason += "; "
					}
					decision.Reason += hookReason
				}
				if pre.Message != "" && pre.Message != hookReason {
					if decision.Reason != "" {
						decision.Reason += "; "
					}
					decision.Reason += pre.Message
				}
			}
			if decision.Reason == "" {
				decision.Reason = "pre-execution policy"
			}
			decision.Prefix = "ghok: denied:"
			if evaluation.Decision == protocol.DecisionDeny {
				decision.Prefix = "ghok: denied by policy:"
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
