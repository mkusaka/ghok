// Package hooks runs trusted executables using the versioned hook protocol.
package hooks

import (
	"context"
	"sort"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
)

const (
	DefaultTimeout              = 10 * time.Second
	DefaultConcurrency          = 1
	DefaultMaxOutputBytes int64 = 1 << 20
	DefaultMaxStderrBytes int64 = 64 << 10
	DefaultPreOnError           = config.OnErrorDeny
	DefaultPostOnError          = config.OnErrorIgnore
	TerminationGrace            = 500 * time.Millisecond
)

type Runner struct {
	Hooks       []config.Hook
	BaseDir     string
	Concurrency int
	Env         []string
	RealGh      string
	Now         func() time.Time
	Redact      func(string) string
}

type Aggregate struct {
	Decision protocol.Decision
	Results  []protocol.HookResult
	Message  string
}

// Select conservatively includes unresolved repository/host matches. Missing
// dependencies remain in the selection so Run can report their skipped outcome.
func (r *Runner) Select(phase string, s policy.Subject) []config.Hook {
	if len(r.Hooks) == 0 {
		return nil
	}
	s.Phase = phase
	var selected []config.Hook
	for _, h := range r.Hooks {
		if h.Phase != phase || h.Enabled != nil && !*h.Enabled {
			continue
		}
		yes, _ := policy.Matches(h.Match, s)
		if !yes {
			yes, _ = policy.MatchMaybe(h.Match, s)
		}
		if yes {
			selected = append(selected, h)
		}
	}
	if len(selected) < 2 {
		return selected
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].Priority < selected[j].Priority })
	// Stable Kahn ordering: priority/config order chooses among ready nodes.
	ordered := make([]config.Hook, 0, len(selected))
	done := make(map[string]bool, len(selected))
	present := make(map[string]bool, len(selected))
	for _, h := range selected {
		present[h.ID] = true
	}
	used := make([]bool, len(selected))
	for len(ordered) < len(selected) {
		found := -1
		for i, h := range selected {
			if used[i] {
				continue
			}
			ready := true
			for _, dep := range h.DependsOn {
				if present[dep] && !done[dep] {
					ready = false
					break
				}
			}
			if ready {
				found = i
				break
			}
		}
		if found < 0 { // Cycles are diagnosed, not executed, by Run.
			for i, h := range selected {
				if !used[i] {
					ordered = append(ordered, h)
				}
			}
			break
		}
		used[found] = true
		ordered = append(ordered, selected[found])
		done[selected[found].ID] = true
	}
	return ordered
}

func effective(phase string, h config.Hook, result protocol.HookResult) protocol.Decision {
	if phase == config.PhasePost {
		return protocol.DecisionAbstain
	}
	if result.Outcome == protocol.OutcomeOK {
		return result.Decision
	}
	if result.Outcome == protocol.OutcomeSkipped {
		return protocol.DecisionAbstain
	}
	switch h.OnError {
	case config.OnErrorAllow:
		return protocol.DecisionAllow
	case config.OnErrorIgnore:
		return protocol.DecisionAbstain
	default:
		return protocol.DecisionDeny
	}
}

func (r *Runner) diagnostic(phase string, h config.Hook, outcome protocol.Outcome, reason string) protocol.HookResult {
	v := protocol.HookResult{HookID: h.ID, Phase: phase, Outcome: outcome, ExitCode: -1, Reason: r.redact(reason)}
	v.Effective = effective(phase, h, v)
	return v
}

func (r *Runner) redact(s string) string {
	if r.Redact != nil {
		return r.Redact(s)
	}
	return s
}
func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run returns results in selection order. A pre denial cancels every remaining
// hook, including already running hooks; post decisions are informational only.
func (r *Runner) Run(ctx context.Context, phase string, hooks []config.Hook, in protocol.Input) Aggregate {
	aggregate := Aggregate{Decision: protocol.DecisionAbstain}
	if len(hooks) == 0 {
		return aggregate
	}
	aggregate.Results = make([]protocol.HookResult, len(hooks))
	concurrency := r.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type completion struct {
		index  int
		result protocol.HookResult
	}
	completed := make(chan completion, len(hooks))
	// 0 pending, 1 running, 2 completed. Only this goroutine touches state.
	state := make([]uint8, len(hooks))
	ids := make(map[string]int, len(hooks))
	duplicate := make(map[string]bool)
	for i, h := range hooks {
		if _, ok := ids[h.ID]; ok {
			duplicate[h.ID] = true
		}
		ids[h.ID] = i
	}
	remaining, running := len(hooks), 0
	denied := false
	finish := func(i int, result protocol.HookResult) {
		state[i] = 2
		aggregate.Results[i] = result
		remaining--
		if phase == config.PhasePre && result.Effective == protocol.DecisionDeny {
			denied = true
			cancel()
		}
	}
	for remaining > 0 {
		progressed := false
		for i, h := range hooks {
			if state[i] != 0 {
				continue
			}
			if denied || runCtx.Err() != nil {
				reason := "context canceled"
				if denied {
					reason = "canceled after pre-hook denial"
				}
				finish(i, r.diagnostic(phase, h, protocol.OutcomeCanceled, reason))
				progressed = true
				continue
			}
			if duplicate[h.ID] {
				finish(i, r.diagnostic(phase, h, protocol.OutcomeSkipped, "duplicate hook ID"))
				progressed = true
				continue
			}
			if h.Enabled != nil && !*h.Enabled || h.Phase != phase {
				finish(i, r.diagnostic(phase, h, protocol.OutcomeSkipped, "disabled hook or phase mismatch"))
				progressed = true
				continue
			}
			ready, skip := true, ""
			for _, dep := range h.DependsOn {
				j, ok := ids[dep]
				if !ok || duplicate[dep] {
					skip = "unknown or ambiguous dependency: " + dep
					break
				}
				if state[j] != 2 {
					ready = false
					continue
				}
				prev := aggregate.Results[j]
				if phase == config.PhasePre && prev.Effective == protocol.DecisionDeny || !h.Optional && prev.Outcome != protocol.OutcomeOK {
					skip = "dependency did not succeed: " + dep
					break
				}
			}
			if skip != "" {
				finish(i, r.diagnostic(phase, h, protocol.OutcomeSkipped, skip))
				progressed = true
				continue
			}
			if !ready || running >= concurrency {
				continue
			}
			input := in
			input.SchemaVersion = 1
			input.Event = phase
			input.HookID = h.ID
			if phase == config.PhasePost && input.Result != nil && !h.CaptureOutput {
				result := *input.Result
				result.Stdout, result.Stderr = nil, nil
				input.Result = &result
			}
			input.Previous = nil
			for _, dep := range h.DependsOn {
				input.Previous = append(input.Previous, aggregate.Results[ids[dep]])
			}
			state[i] = 1
			running++
			progressed = true
			go func(i int, h config.Hook, input protocol.Input) {
				completed <- completion{i, r.execute(runCtx, phase, h, input)}
			}(i, h, input)
		}
		if remaining == 0 {
			break
		}
		if running == 0 {
			if !progressed {
				for i, h := range hooks {
					if state[i] == 0 {
						finish(i, r.diagnostic(phase, h, protocol.OutcomeSkipped, "dependency cycle"))
					}
				}
			}
			continue
		}
		c := <-completed
		running--
		finish(c.index, c.result)
	}
	// First effective deny in selection order supplies the user diagnostic.
	for _, v := range aggregate.Results {
		if phase == config.PhasePre && v.Effective == protocol.DecisionDeny {
			aggregate.Decision = protocol.DecisionDeny
			aggregate.Message = v.Message
			if aggregate.Message == "" {
				aggregate.Message = v.Reason
			}
			if aggregate.Message == "" {
				aggregate.Message = v.Error
			}
			return aggregate
		}
		if v.Effective == protocol.DecisionAllow {
			aggregate.Decision = protocol.DecisionAllow
		}
	}
	return aggregate
}
