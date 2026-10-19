package policy

import (
	"fmt"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

const (
	DefaultMode             = config.ModeObserve
	DefaultUnknownMutation  = config.UnknownAudit
	DefaultUnknownInput     = config.UnknownAudit
	DefaultProtectedUnknown = config.UnknownDeny
)

type Decision = protocol.Decision

type OpEvaluation struct {
	Index    int               `json:"index"`
	Decision protocol.Decision `json:"decision"`
	Reasons  []string          `json:"reasons,omitempty"`
}

type DenialKind string

const (
	DenialRule   DenialKind = "rule"
	DenialPolicy DenialKind = "policy"
)

// Denial carries presentation provenance without changing serialized policy logs.
type Denial struct {
	Kind   DenialKind
	ID     string
	Reason string
}

type Evaluation struct {
	Mode      string             `json:"mode"`
	Decision  protocol.Decision  `json:"decision"`
	WouldDeny bool               `json:"wouldDeny"`
	Reasons   []string           `json:"reasons,omitempty"`
	Warnings  []protocol.Warning `json:"warnings,omitempty"`
	// DenyReasons contains only deny-causing reasons, even in observe mode.
	// Operation-scoped reasons include the action; Reasons retains diagnostics.
	DenyReasons []string       `json:"denyReasons,omitempty"`
	Rules       []string       `json:"rules,omitempty"`
	PerOp       []OpEvaluation `json:"perOp,omitempty"`
	Denials     []Denial       `json:"-"`
}

func merge(a, b protocol.Decision) protocol.Decision {
	if a == protocol.DecisionDeny || b == protocol.DecisionDeny {
		return protocol.DecisionDeny
	}
	if a == protocol.DecisionAudit || b == protocol.DecisionAudit {
		return protocol.DecisionAudit
	}
	return protocol.DecisionAllow
}
func handling(value, fallback string) protocol.Decision {
	switch value {
	case config.UnknownAllow:
		return protocol.DecisionAllow
	case config.UnknownDeny:
		return protocol.DecisionDeny
	case config.UnknownAudit:
		return protocol.DecisionAudit
	default:
		return protocol.Decision(fallback)
	}
}
func unclassified(op model.Operation) bool {
	return op.Effect == model.EffectUnknown || op.Effect == "" || op.Family == model.FamilyUnknown && op.Effect != model.EffectRead
}

// Evaluate conservatively applies repository/host-constrained rules whose
// targets are unresolved. No allow rule can override a deny or audit.
func Evaluate(p config.Policy, s Subject) Evaluation {
	mode := p.Mode
	if mode != config.ModeEnforce {
		mode = DefaultMode
	}
	e := Evaluation{Mode: mode, Decision: protocol.DecisionAllow}
	perIndex := make(map[int]int, len(s.Result.Operations))
	add := func(index int, decision protocol.Decision, reason string, kind DenialKind, id string) {
		v := &e.PerOp[perIndex[index]]
		v.Decision = merge(v.Decision, decision)
		v.Reasons = append(v.Reasons, reason)
		e.Reasons = append(e.Reasons, fmt.Sprintf("operation %d: %s", index, reason))
		if decision == protocol.DecisionDeny {
			denyReason := fmt.Sprintf("%s (%s)", reason, s.Result.Operations[index].Action)
			e.DenyReasons = append(e.DenyReasons, denyReason)
			if kind != DenialRule {
				e.Denials = append(e.Denials, Denial{Kind: kind, ID: id, Reason: denyReason})
			}
		}
	}
	if s.Result.Classification.MutationFree {
		e.Reasons = append(e.Reasons, "normalization proved mutation-free")
	} else {
		e.Reasons = append(e.Reasons, "normalization did not prove mutation-free")
	}
	e.Reasons = append(e.Reasons, s.Result.Errors...)
	for i, op := range s.Result.Operations {
		if !considered(op) {
			reason := fmt.Sprintf("operation %d ignored (%s)", i, op.Execution)
			if op.Evidence != "" {
				reason += ": " + op.Evidence
			}
			e.Reasons = append(e.Reasons, reason)
			continue
		}
		perIndex[i] = len(e.PerOp)
		e.PerOp = append(e.PerOp, OpEvaluation{Index: i, Decision: protocol.DecisionAllow})
		if op.Execution == model.ExecutionMaybe {
			add(i, protocol.DecisionAllow, "operation may execute: "+op.Evidence, "", "")
		}
		if op.UnknownReason != model.UnknownNone {
			add(i, protocol.DecisionAllow, "unknown classification: "+string(op.UnknownReason)+"; "+op.Detail, "", "")
		}
		if repo, _ := targetValues(op, s.Context); repo == "" && op.Effect != model.EffectRead {
			add(i, protocol.DecisionAllow, "target repository unresolved", "", "")
		}
		if len(op.Unresolved) != 0 {
			fields := make([]string, 0, len(op.Unresolved))
			for _, u := range op.Unresolved {
				fields = append(fields, u.Field+": "+u.Reason)
			}
			add(i, protocol.DecisionAllow, "unresolved: "+strings.Join(fields, "; "), "", "")
		}
	}
	// Rule and operation iteration order is independent of map ordering.
	for _, rule := range p.Rules {
		if !invocationMatches(rule.Match, s) {
			continue
		}
		matched := false
		decision := handling(rule.Decision, config.UnknownAllow)
		if len(s.Result.Operations) == 0 {
			if yes, _ := Matches(rule.Match, rule.Unless, s); yes {
				matched = true
				e.Decision = merge(e.Decision, decision)
				e.Reasons = append(e.Reasons, "rule "+rule.ID+": "+rule.Reason)
				if decision == protocol.DecisionDeny {
					e.DenyReasons = append(e.DenyReasons, "rule "+rule.ID+": "+rule.Reason)
					e.Denials = append(e.Denials, Denial{Kind: DenialRule, ID: rule.ID, Reason: rule.Reason})
				}
			}
		}
		for i, op := range s.Result.Operations {
			yes, maybe := opMatch(rule.Match, s, op)
			if !yes || maybe && rule.OnUnresolved == config.OnErrorIgnore || excluded(rule.Unless, s, &op) {
				continue
			}
			matched = true
			reason := "rule " + rule.ID + ": " + rule.Reason
			denialReason := rule.Reason
			if maybe {
				reason += "; matched conservatively (target unresolved)"
				denialReason += "; matched conservatively (target unresolved)"
			}
			add(i, decision, reason, DenialRule, rule.ID)
			if decision == protocol.DecisionDeny {
				e.Denials = append(e.Denials, Denial{Kind: DenialRule, ID: rule.ID, Reason: fmt.Sprintf("%s (%s)", denialReason, op.Action)})
			}
		}
		if matched {
			e.Rules = append(e.Rules, rule.ID)
			if rule.Decision == "warn" {
				e.Warnings = append(e.Warnings, protocol.Warning{Source: "rule", ID: rule.ID, Text: rule.Reason})
			}
		}
	}
	for i, op := range s.Result.Operations {
		if !considered(op) {
			continue
		}
		unknown := unclassified(op)
		if op.UnknownReason == model.UnknownInputUnavailable || op.UnknownReason == model.UnknownInputTooLarge {
			cause := string(op.UnknownReason)
			if op.Detail != "" {
				cause += ": " + op.Detail
			}
			add(i, handling(p.UnknownInput, DefaultUnknownInput), "unknownInput policy: "+cause, DenialPolicy, "unknownInput")
		} else if unknown {
			add(i, handling(p.UnknownMutation, DefaultUnknownMutation), "unclassified-write: unknownMutation policy", DenialPolicy, "unknownMutation")
		}
		if len(p.ProtectedRepositories) == 0 || op.Effect == model.EffectRead {
			continue
		}
		repo, _ := targetValues(op, s.Context)
		if repo == "" {
			add(i, handling(p.ProtectedUnknown, DefaultProtectedUnknown), "target repository unresolved; cannot prove it is not protected", DenialPolicy, "protectedUnknown")
		} else if unknown && anyMatch(p.ProtectedRepositories, repo, MatchRepo) {
			add(i, handling(p.ProtectedUnknown, DefaultProtectedUnknown), "unclassified-write targets protected repository "+repo, DenialPolicy, "protectedRepositories")
		}
	}
	for _, op := range e.PerOp {
		e.Decision = merge(e.Decision, op.Decision)
	}
	e.WouldDeny = e.Decision == protocol.DecisionDeny
	if mode == config.ModeObserve {
		if e.WouldDeny {
			e.Decision = protocol.DecisionAudit
		}
		for i := range e.PerOp {
			if e.PerOp[i].Decision == protocol.DecisionDeny {
				e.PerOp[i].Decision = protocol.DecisionAudit
			}
		}
	}
	return e
}
