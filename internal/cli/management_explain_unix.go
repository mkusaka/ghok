//go:build unix

package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/redact"
)

func managementExplain(a *managementAnalysis, showSecrets bool) int {
	var output bytes.Buffer
	r := redact.New(a.env)
	result := a.result
	if !showSecrets {
		result = r.Result(result)
	}
	evaluation := a.evaluation
	fmt.Fprintf(&output, "Decision: %s (mode=%s)\n", evaluation.Decision, evaluation.Mode)
	seenReasons := make(map[string]bool)
	for _, reason := range evaluation.DenyReasons {
		if !seenReasons[reason] {
			fmt.Fprintln(&output, "  Deny reason:", reason)
			seenReasons[reason] = true
		}
	}
	runner := hooks.Runner{Hooks: a.config.Hooks}
	for _, phase := range []string{config.PhasePre, config.PhasePost} {
		selected := runner.Select(phase, a.subject(phase))
		fmt.Fprintf(&output, "  Hooks that WOULD run (%s):", phase)
		if len(selected) == 0 {
			fmt.Fprintln(&output, " none")
			continue
		}
		fmt.Fprintln(&output)
		present := make(map[string]bool, len(selected))
		for _, h := range selected {
			present[h.ID] = true
		}
		for i, h := range selected {
			yes, _ := policy.Matches(h.Match, a.subject(phase))
			fmt.Fprintf(&output, "    %s: order=%d", h.ID, i+1)
			if !yes {
				fmt.Fprint(&output, " (maybe-match: repository or host unresolved)")
			}
			for _, dependency := range h.DependsOn {
				if !present[dependency] {
					fmt.Fprintf(&output, " (skipped: dependency %s not selected)", dependency)
				}
			}
			fmt.Fprintln(&output)
		}
	}
	if showSecrets {
		fmt.Fprintln(&output, "WARNING: --show-secrets is enabled; this output may contain credentials.")
	}
	fmt.Fprintln(&output, "\nOperations:")
	classification := result.Classification
	fmt.Fprintf(&output, "  Classification: level=%s confidence=%s resolution=%s mutation-free=%t\n", classification.Level, classification.Confidence, classification.Resolution, classification.MutationFree)
	fmt.Fprintln(&output, "  Source:", managementCompact(result.Source))
	for i, op := range result.Operations {
		fmt.Fprintf(&output, "  Operation %d: %s (family=%s effect=%s execution=%s)\n", i, op.Action, op.Family, op.Effect, op.Execution)
		fmt.Fprintln(&output, "    Target:", managementCompact(op.Target))
		if len(op.Parameters) != 0 {
			fmt.Fprintln(&output, "    Parameters:", managementCompact(op.Parameters))
		}
		if op.Detail != "" {
			fmt.Fprintln(&output, "    Detail:", op.Detail)
		}
		if op.UnknownReason != "" {
			fmt.Fprintln(&output, "    Unknown reason:", op.UnknownReason)
		}
		if op.Evidence != "" {
			fmt.Fprintln(&output, "    Evidence:", op.Evidence)
		}
		managementUnresolved(&output, "    ", op.Unresolved)
	}
	for _, err := range result.Errors {
		fmt.Fprintln(&output, "  Normalization error:", err)
		seenReasons[err] = true
	}
	for _, diagnostic := range a.diagnostics {
		fmt.Fprintln(&output, "  Resolution diagnostic:", diagnostic)
	}
	fmt.Fprintln(&output, "\nPolicy:")
	if len(evaluation.Rules) == 0 {
		fmt.Fprintln(&output, "  Matched rules: none")
	} else {
		fmt.Fprintln(&output, "  Matched rules:")
		for _, rule := range a.config.Policy.Rules {
			yes, indexes := policy.Matches(rule.Match, a.subject(""))
			maybe, candidates := policy.MatchMaybe(rule.Match, a.subject(""))
			if yes || maybe {
				fmt.Fprintf(&output, "    %s: decision=%s match=%t operations=%v maybe-match=%t maybe-operations=%v\n", rule.ID, rule.Decision, yes, indexes, maybe, candidates)
			}
		}
	}
	for _, opEvaluation := range evaluation.PerOp {
		op := a.result.Operations[opEvaluation.Index]
		for _, reason := range opEvaluation.Reasons {
			if seenReasons[reason+" ("+op.Action+")"] || managementOperationReasonShown(op, reason) {
				continue
			}
			key := fmt.Sprintf("operation %d: %s", opEvaluation.Index, reason)
			if !seenReasons[key] {
				fmt.Fprintln(&output, "  Reason:", key)
				seenReasons[key] = true
			}
		}
	}
	for _, reason := range evaluation.Reasons {
		if seenReasons[reason] || strings.HasPrefix(reason, "operation ") || strings.HasPrefix(reason, "normalization ") {
			continue
		}
		fmt.Fprintln(&output, "  Reason:", reason)
		seenReasons[reason] = true
	}
	for _, note := range a.notes {
		fmt.Fprintln(&output, "Config note:", note)
	}
	fmt.Fprintln(&output, "\nPolicy only; hook outcomes and gh results are unknown. No hooks or original gh command were executed.")
	if evaluation.Mode == config.ModeObserve {
		fmt.Fprintln(&output, "Observe mode never blocks; denials are recorded as would-deny.")
	} else if evaluation.WouldDeny {
		fmt.Fprintln(&output, "Enforce denial prevents gh and post hooks; hooks above show match selection only.")
	}
	fmt.Fprintln(&output, "Explain exits 0. ghok is not a security boundary; direct gh/API calls bypass it.")
	text := output.String()
	if !showSecrets {
		text = r.String(text)
	}
	if _, err := io.WriteString(os.Stdout, text); err != nil {
		return managementFailure(err)
	}
	return 0
}

func managementUnresolved(w io.Writer, indent string, values []model.Unresolved) {
	for i, value := range values {
		// Repeated analysis entries should not repeat the same field/reason.
		duplicate := false
		for _, previous := range values[:i] {
			if previous == value {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		_, _ = fmt.Fprintf(w, "%sUnresolved %s: %s", indent, value.Field, value.Reason)
		if value.Source != "" {
			_, _ = fmt.Fprintf(w, " (source=%s)", value.Source)
		}
		_, _ = fmt.Fprintln(w)
	}
}

func managementPrintMatch(w io.Writer, indent string, match config.Match, r *redact.Redactor) {
	text := managementCompact(match)
	if r != nil {
		text = r.String(text)
	}
	_, _ = fmt.Fprintln(w, indent+"criteria: "+text)
}

// Policy includes operation diagnostics as prose; the operation section already
// carries their structured fields, reasons and evidence.
func managementOperationReasonShown(op model.Operation, reason string) bool {
	if len(op.Unresolved) != 0 && strings.HasPrefix(reason, "unresolved: ") {
		return true
	}
	if reason == "unknown classification: "+string(op.UnknownReason)+"; "+op.Detail ||
		reason == "operation may execute: "+op.Evidence {
		return true
	}
	if reason == "target repository unresolved" {
		for _, unresolved := range op.Unresolved {
			if unresolved.Field == "target.repository" {
				return true
			}
		}
	}
	return false
}
