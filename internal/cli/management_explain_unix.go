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
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
)

func managementExplain(a *managementAnalysis, showSecrets bool) int {
	var output bytes.Buffer
	r := a.redactor
	display := func(text string) string {
		if !showSecrets || a.envEdited {
			text = r.String(text)
		}
		return terminalText(text)
	}
	result := a.result
	if !showSecrets || a.envEdited {
		result = r.Result(result)
	}
	evaluation := a.evaluation
	fmt.Fprintf(&output, "Decision: %s (mode=%s)\n", evaluation.Decision, evaluation.Mode)
	managementPrintTransformed(&output, a, showSecrets)
	warningRedactor := r.String
	if showSecrets && !a.envEdited {
		warningRedactor = func(s string) string { return s }
	}
	managementPrintWarnings(&output, a.warnings(), warningRedactor)
	seenReasons := make(map[string]bool)
	for _, reason := range evaluation.DenyReasons {
		if !seenReasons[reason] {
			fmt.Fprintln(&output, display("  Deny reason: "+reason))
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
			yes, _ := policy.Matches(h.Match, h.Unless, a.subject(phase))
			fmt.Fprintf(&output, "    %s: order=%d", display(h.ID), i+1)
			if !yes {
				fmt.Fprint(&output, " (maybe-match: repository or host unresolved)")
			}
			for _, dependency := range h.DependsOn {
				if !present[dependency] {
					fmt.Fprintf(&output, " (skipped: dependency %s not selected)", display(dependency))
				}
			}
			fmt.Fprintln(&output)
		}
	}
	for _, h := range a.config.IgnoredHooks {
		fmt.Fprintln(&output, display(fmt.Sprintf("  %s: ignored (%s)", h.ID, h.Reason)))
		fmt.Fprintln(&output, display("  "+ignoredHookDescription(h)))
	}
	if showSecrets {
		fmt.Fprintln(&output, "WARNING: --show-secrets is enabled; this output may contain credentials.")
	}
	fmt.Fprintln(&output, "\nOperations:")
	classification := result.Classification
	fmt.Fprintf(&output, "  Classification: level=%s confidence=%s resolution=%s mutation-free=%t\n", classification.Level, classification.Confidence, classification.Resolution, classification.MutationFree)
	fmt.Fprintln(&output, display("  Source: "+managementCompact(result.Source)))
	for i, op := range result.Operations {
		fmt.Fprintln(&output, display(fmt.Sprintf("  Operation %d: %s (family=%s effect=%s execution=%s)", i, op.Action, op.Family, op.Effect, op.Execution)))
		fmt.Fprintln(&output, display("    Target: "+managementCompact(op.Target)))
		if len(op.Parameters) != 0 {
			fmt.Fprintln(&output, display("    Parameters: "+managementCompact(op.Parameters)))
		}
		if op.Detail != "" {
			fmt.Fprintln(&output, display("    Detail: "+op.Detail))
		}
		if op.UnknownReason != "" {
			fmt.Fprintln(&output, display("    Unknown reason: "+string(op.UnknownReason)))
		}
		if op.Evidence != "" {
			fmt.Fprintln(&output, display("    Evidence: "+op.Evidence))
		}
		managementUnresolved(&output, "    ", op.Unresolved)
	}
	for _, err := range result.Errors {
		fmt.Fprintln(&output, display("  Normalization error: "+err))
		seenReasons[err] = true
	}
	for _, diagnostic := range a.diagnostics {
		fmt.Fprintln(&output, display("  Resolution diagnostic: "+diagnostic))
	}
	fmt.Fprintln(&output, "\nPolicy:")
	if len(evaluation.Rules) == 0 {
		fmt.Fprintln(&output, "  Matched rules: none")
	} else {
		fmt.Fprintln(&output, "  Matched rules:")
		for _, rule := range a.config.Policy.Rules {
			yes, indexes := policy.Matches(rule.Match, rule.Unless, a.subject(""))
			maybe, candidates := policy.MatchMaybe(rule.Match, rule.Unless, a.subject(""))
			if yes || maybe {
				fmt.Fprintln(&output, display(fmt.Sprintf("    %s: decision=%s match=%t operations=%v maybe-match=%t maybe-operations=%v", rule.ID, rule.Decision, yes, indexes, maybe, candidates)))
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
				fmt.Fprintln(&output, display("  Reason: "+key))
				seenReasons[key] = true
			}
		}
	}
	for _, reason := range evaluation.Reasons {
		if seenReasons[reason] || strings.HasPrefix(reason, "operation ") || strings.HasPrefix(reason, "normalization ") {
			continue
		}
		fmt.Fprintln(&output, display("  Reason: "+reason))
		seenReasons[reason] = true
	}
	for _, note := range a.notes {
		fmt.Fprintln(&output, display("Config note: "+note))
	}
	fmt.Fprintln(&output, "\nPolicy only; hook outcomes and gh results are unknown. No hooks or original gh command were executed.")
	if evaluation.Mode == config.ModeObserve {
		fmt.Fprintln(&output, "Observe mode never blocks; denials are recorded as would-deny.")
	} else if evaluation.WouldDeny {
		fmt.Fprintln(&output, "Enforce denial prevents gh and post hooks; hooks above show match selection only.")
	}
	fmt.Fprintln(&output, "Explain exits 0. ghok is not a security boundary; direct gh/API calls bypass it.")
	text := output.String()
	if a.envEdited {
		text = r.String(text)
	}
	if _, err := io.WriteString(os.Stdout, text); err != nil {
		return managementFailure(err)
	}
	return 0
}

func managementPrintWarnings(w io.Writer, warnings []protocol.Warning, redact func(string) string) {
	if len(warnings) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "Warnings:")
	for _, warning := range warnings {
		entry := "  " + warning.Source + " " + strings.ReplaceAll(redact(warning.ID), "\n", `\n`) + ":\n    " + strings.ReplaceAll(redact(warning.Text), "\n", "\n    ")
		_, _ = fmt.Fprintln(w, terminalText(entry))
	}
}

func managementExplainJSON(a *managementAnalysis, showSecrets bool) int {
	value := struct {
		Normalization    model.Result           `json:"normalization"`
		Context          *model.ResolvedContext `json:"context,omitempty"`
		Policy           policy.Evaluation      `json:"policy"`
		Hooks            map[string][]string    `json:"hooks"`
		Warnings         []protocol.Warning     `json:"warnings,omitempty"`
		Transformed      *managementTransformed `json:"transformed,omitempty"`
		TransformResults []protocol.HookResult  `json:"transformResults,omitempty"`
		Notes            []string               `json:"notes,omitempty"`
		Diagnostics      []string               `json:"diagnostics,omitempty"`
	}{
		Normalization: a.result, Context: a.context, Policy: a.evaluation,
		Hooks: make(map[string][]string), Warnings: a.warnings(),
		Transformed: a.transformed(), TransformResults: a.transformResults,
		Notes: a.notes, Diagnostics: a.diagnostics,
	}
	runner := hooks.Runner{Hooks: a.config.Hooks}
	for _, phase := range []string{config.PhasePre, config.PhasePost} {
		ids := []string{}
		for _, hook := range runner.Select(phase, a.subject(phase)) {
			ids = append(ids, hook.ID)
		}
		value.Hooks[phase] = ids
	}
	if showSecrets && !a.envEdited {
		return managementJSON(value, nil)
	}
	display, err := managementRedacted(value, a.redactor)
	if err != nil {
		return managementFailure(err)
	}
	if value.Transformed != nil {
		display.(map[string]any)["transformed"] = value.Transformed
	}
	return managementJSON(display, nil)
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
		text := fmt.Sprintf("%sUnresolved %s: %s", indent, value.Field, value.Reason)
		if value.Source != "" {
			text += " (source=" + value.Source + ")"
		}
		_, _ = fmt.Fprintln(w, terminalText(text))
	}
}

func managementPrintMatch(w io.Writer, indent string, match config.Match, r *redact.Redactor) {
	text := managementCompact(match)
	if r != nil {
		text = r.String(text)
	}
	_, _ = fmt.Fprintln(w, terminalText(indent+"criteria: "+text))
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
