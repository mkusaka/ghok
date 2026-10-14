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
	local := a.context
	if !showSecrets {
		result = r.Result(result)
		input := r.Input(hookInput(a.invocation.Argv, a.cwd, a.real, a.result, a.context, a.evaluation, ""))
		local = input.Context
	}
	classification := result.Classification
	fmt.Fprintf(&output, "Classification: level=%s confidence=%s resolution=%s mutation-free=%t\n", classification.Level, classification.Confidence, classification.Resolution, classification.MutationFree)
	fmt.Fprintln(&output, "Source:", managementCompact(result.Source))
	if showSecrets {
		fmt.Fprintln(&output, "WARNING: --show-secrets is enabled; this output may contain credentials.")
	} else {
		fmt.Fprintln(&output, "Secrets: redacted (use --show-secrets only in a trusted terminal).")
	}
	for _, note := range a.notes {
		fmt.Fprintln(&output, "Config note:", note)
	}
	for _, diagnostic := range a.diagnostics {
		fmt.Fprintln(&output, "Resolution diagnostic:", diagnostic)
	}
	if local != nil {
		fmt.Fprintln(&output, "Local context:", managementCompact(local))
		managementUnresolved(&output, "  ", local.Unresolved)
	} else {
		fmt.Fprintln(&output, "Resolution: not requested; no local git or API resolution performed.")
	}
	for _, err := range result.Errors {
		fmt.Fprintln(&output, "Normalization error:", err)
	}
	fmt.Fprintf(&output, "Operations (%d):\n", len(result.Operations))
	for i, op := range result.Operations {
		fmt.Fprintf(&output, "  Operation %d: %s (family=%s effect=%s execution=%s)\n", i, op.Action, op.Family, op.Effect, op.Execution)
		fmt.Fprintln(&output, "    Target:", managementCompact(op.Target))
		fmt.Fprintln(&output, "    Origin:", managementCompact(op.Origin))
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
	evaluation := a.evaluation
	fmt.Fprintf(&output, "Policy: mode=%s decision=%s would-deny=%t\n", evaluation.Mode, evaluation.Decision, evaluation.WouldDeny)
	if len(evaluation.Rules) == 0 {
		fmt.Fprintln(&output, "  Matched rules: none")
	} else {
		fmt.Fprintln(&output, "  Matched rules:", managementCompact(evaluation.Rules))
	}
	if len(evaluation.DenyReasons) == 0 {
		fmt.Fprintln(&output, "  Deny reasons: none")
	} else {
		for _, reason := range evaluation.DenyReasons {
			fmt.Fprintln(&output, "  Deny reason:", reason)
		}
	}
	for _, reason := range evaluation.Reasons {
		fmt.Fprintln(&output, "  Reason:", reason)
	}
	for _, op := range evaluation.PerOp {
		fmt.Fprintf(&output, "  Operation %d policy: %s\n", op.Index, op.Decision)
		for _, reason := range op.Reasons {
			fmt.Fprintln(&output, "    Reason:", reason)
		}
	}
	fmt.Fprintln(&output, "Rule match details:")
	if len(a.config.Policy.Rules) == 0 {
		fmt.Fprintln(&output, "  No declarative rules configured.")
	}
	for _, rule := range a.config.Policy.Rules {
		fmt.Fprintf(&output, "  Rule %s: decision=%s reason=%s\n", rule.ID, rule.Decision, rule.Reason)
		managementDescribeMatch(&output, "    ", rule.Match, a.subject(""))
	}
	runner := hooks.Runner{Hooks: a.config.Hooks}
	for _, phase := range []string{config.PhasePre, config.PhasePost} {
		selected := runner.Select(phase, a.subject(phase))
		order := make(map[string]int, len(selected))
		for i, hook := range selected {
			order[hook.ID] = i + 1
		}
		fmt.Fprintf(&output, "Hooks that WOULD run (%s):\n", phase)
		if len(selected) == 0 {
			fmt.Fprintln(&output, "  None.")
		}
		for _, h := range a.config.Hooks {
			if h.Phase != phase {
				continue
			}
			if h.Enabled != nil && !*h.Enabled {
				fmt.Fprintf(&output, "  %s: would not run (disabled)\n", h.ID)
				continue
			}
			if index, ok := order[h.ID]; ok {
				fmt.Fprintf(&output, "  %s: WOULD run, order=%d priority=%d timeout=%s onError=%s\n", h.ID, index, h.Priority, h.Timeout.D(), h.OnError)
			} else {
				fmt.Fprintf(&output, "  %s: would not run (match criteria do not all hold on the same operation)\n", h.ID)
			}
			managementDescribeMatch(&output, "    ", h.Match, a.subject(phase))
			if len(h.DependsOn) != 0 {
				fmt.Fprintln(&output, "    Dependencies:", strings.Join(h.DependsOn, ", "))
				for _, dependency := range h.DependsOn {
					if _, present := order[dependency]; !present {
						fmt.Fprintf(&output, "    Dependency %s is not selected; runner will report skipped.\n", dependency)
					}
				}
			}
		}
	}
	fmt.Fprintln(&output, "No hooks or original gh command were executed. Hook decisions cannot be predicted without running hooks.")
	final := "allow"
	if evaluation.Decision == "deny" {
		final = "deny"
	}
	fmt.Fprintf(&output, "Final decision: %s (policy only; pre-hook outcomes remain unknown).\n", final)
	if evaluation.Mode == config.ModeObserve {
		fmt.Fprintln(&output, "Observe mode never blocks: policy/hook denials are recorded as would-deny.")
	}
	fmt.Fprintln(&output, "Explain exits 0 even when policy denies. ghok is not a security boundary; direct gh/API calls bypass it.")
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
	for _, value := range values {
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

func managementDescribeMatch(w io.Writer, indent string, match config.Match, subject policy.Subject) {
	yes, indexes := policy.Matches(match, subject)
	maybe, candidates := policy.MatchMaybe(match, subject)
	_, _ = fmt.Fprintf(w, "%smatch=%t operations=%v maybe-match=%t maybe-operations=%v\n", indent, yes, indexes, maybe, candidates)
	if maybe {
		_, _ = fmt.Fprintln(w, indent+"Maybe-match is conservatively selected because repository or host is unresolved; it is not a definite match.")
	}
	managementPrintMatch(w, indent, match, nil)
	criteria := []struct {
		name  string
		match config.Match
		set   bool
	}{
		{"operation", config.Match{Operations: match.Operations}, len(match.Operations) != 0},
		{"command", config.Match{Commands: match.Commands}, len(match.Commands) != 0},
		{"repositories", config.Match{Repositories: match.Repositories}, len(match.Repositories) != 0},
		{"hosts", config.Match{Hosts: match.Hosts}, len(match.Hosts) != 0},
		{"effect", config.Match{Effects: match.Effects}, len(match.Effects) != 0},
		{"level", config.Match{Levels: match.Levels}, len(match.Levels) != 0},
	}
	for _, criterion := range criteria {
		if !criterion.set {
			continue
		}
		matched, operations := policy.Matches(criterion.match, subject)
		possible, unresolved := policy.MatchMaybe(criterion.match, subject)
		_, _ = fmt.Fprintf(w, "%s%s: match=%t operations=%v maybe=%t operations=%v\n", indent, criterion.name, matched, operations, possible, unresolved)
	}
	if len(subject.Result.Operations) != 0 && !yes && !maybe {
		_, _ = fmt.Fprintln(w, indent+"All criteria are ANDed and must hold on one considered operation; skipped/not-selected operations do not match.")
	}
}
