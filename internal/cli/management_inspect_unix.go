//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/install"
	"github.com/mkusaka/ghok/internal/logging"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/process"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
	"github.com/mkusaka/ghok/internal/resolve"
)

type managementAnalysis struct {
	config      *config.Config
	notes       []string
	cwd         string
	env         map[string]string
	invocation  model.Invocation
	result      model.Result
	context     *model.ResolvedContext
	evaluation  policy.Evaluation
	diagnostics []string
	real        string
}

func managementAnalyze(args []string, resolveTargets, allowAPI bool) (*managementAnalysis, error) {
	env := environment()
	c, notes, cwd, err := managementLoad(env)
	if err != nil {
		return nil, err
	}
	inv := invocation(c, args, env, cwd, model.StdinData{Status: model.StdinNotCaptured})
	if needed, _ := normalize.NeedsStdin(expandedArgv(inv)); needed {
		if install.IsTTY(os.Stdin) {
			inv.Stdin.Status = model.StdinTTY
		} else {
			spool, err := process.SpoolStdin(os.Stdin, c.Runtime.StdinMaxBytes, c.Runtime.StdinTimeout.D())
			if spool != nil {
				defer func() { _ = spool.Close() }()
				inv.Stdin = spool.Data()
			}
			if err != nil && spool == nil {
				return nil, err
			}
		}
	}
	a := &managementAnalysis{config: c, notes: notes, cwd: cwd, env: env, invocation: inv, result: normalize.Normalize(inv)}
	if resolveTargets {
		if allowAPI {
			a.real, err = realGh(c, env)
			if err != nil {
				return nil, err
			}
		}
		r := &resolve.Resolver{Env: env, Cwd: cwd, RealGh: a.real, Timeout: c.Runtime.ResolveTimeout.D()}
		local := r.Local()
		a.context = &local
		var need []string
		seen := make(map[string]bool)
		for _, op := range a.result.Operations {
			for _, unresolved := range op.Unresolved {
				if !seen[unresolved.Field] {
					seen[unresolved.Field] = true
					need = append(need, unresolved.Field)
				}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.Runtime.ResolveTimeout.D())
		a.diagnostics = r.Fill(ctx, &a.result, need, allowAPI)
		cancel()
	}
	a.evaluation = policy.Evaluate(c.Policy, a.subject(""))
	return a, nil
}

func (a *managementAnalysis) subject(phase string) policy.Subject {
	return policy.Subject{Result: a.result, Argv: a.result.Source.Argv, Context: a.context, Phase: phase, Token: policy.TokenKind(a.env), Env: a.env}
}

type managementInspectOptions struct {
	noResolve   bool
	allowAPI    bool
	showSecrets bool
}

func managementInspect(command string, args []string, options managementInspectOptions) int {
	if options.allowAPI && options.noResolve {
		return managementUsageError("--allow-api cannot be combined with --no-resolve")
	}
	a, err := managementAnalyze(args, !options.noResolve, options.allowAPI)
	if err != nil {
		return managementFailure(err)
	}
	r := redact.New(a.env)
	if command == "normalize" {
		for _, diagnostic := range a.diagnostics {
			fmt.Fprintln(os.Stderr, "ghok:", r.String(diagnostic))
		}
		return managementJSON(r.Result(a.result), nil)
	}
	return managementExplain(a, options.showSecrets)
}

func managementHooksList(asJSON bool) int {
	env := environment()
	c, notes, _, err := managementLoad(env)
	if err != nil {
		return managementFailure(err)
	}
	r := redact.New(env)
	// Command flags need contextual redaction in addition to JSON key redaction.
	type listedHook struct {
		config.Hook
		Ignored string `json:"ignored,omitempty"`
	}
	display := make([]listedHook, 0, len(c.Hooks)+len(c.IgnoredHooks))
	for _, h := range c.Hooks {
		h.Command = r.Argv(h.Command)
		display = append(display, listedHook{Hook: h})
	}
	for _, h := range c.IgnoredHooks {
		h.Command = r.Argv(h.Command)
		display = append(display, listedHook{Hook: h.Hook, Ignored: h.Reason})
	}
	if asJSON {
		return managementJSON(display, r)
	}
	if len(display) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No hooks configured.")
	}
	for _, h := range display {
		enabled := h.Enabled == nil || *h.Enabled
		_, _ = fmt.Fprintf(os.Stdout, "%s\n", r.String(fmt.Sprintf("%s phase=%s enabled=%t priority=%d timeout=%s onError=%s optional=%t captureOutput=%t", h.ID, h.Phase, enabled, h.Priority, h.Timeout.D(), h.OnError, h.Optional, h.CaptureOutput)))
		if h.Ignored != "" {
			_, _ = fmt.Fprintln(os.Stdout, r.String("  ignored ("+h.Ignored+")"))
		}
		_, _ = fmt.Fprintln(os.Stdout, r.String("  source: "+h.Source))
		_, _ = fmt.Fprintln(os.Stdout, r.String("  command: "+managementCompact(h.Command)))
		managementPrintMatch(os.Stdout, "  ", h.Match, r)
		if len(h.DependsOn) != 0 {
			_, _ = fmt.Fprintln(os.Stdout, r.String("  dependsOn: "+strings.Join(h.DependsOn, ", ")))
		}
	}
	for _, note := range notes {
		_, _ = fmt.Fprintln(os.Stdout, r.String("Note: "+note))
	}
	return 0
}

func managementHooksTest(args []string, phase string, exitCode int, asJSON bool, options managementInspectOptions, expect, expectHooks, expectPolicy string) int {
	if phase != config.PhasePre && phase != config.PhasePost {
		return managementUsageError("--phase must be pre or post")
	}
	if options.allowAPI && options.noResolve {
		return managementUsageError("--allow-api cannot be combined with --no-resolve")
	}
	a, err := managementAnalyze(args, !options.noResolve, options.allowAPI)
	if err != nil {
		return managementFailure(err)
	}
	// Discovery supplies hook helpers a real-gh path when available, but a pure
	// hook test remains usable on machines without gh. No original gh is started.
	a.real, _ = realGh(a.config, a.env)
	r := redact.New(a.env)
	runner := hooks.Runner{Hooks: a.config.Hooks, BaseDir: a.config.Dir, Concurrency: a.config.Runtime.HookConcurrency, Env: os.Environ(), RealGh: a.real, Redact: r.String}
	if exitCode < 0 || exitCode > 255 {
		return managementUsageError("--exit-code must be 0-255")
	}
	subject := a.subject(phase)
	input := hookInput(args, a.cwd, a.real, a.result, a.context, a.evaluation, logging.NewInvocationID())
	if phase == config.PhasePost {
		// A synthetic execution result, not a real one; it also drives
		// match.result / match.exitCode selection.
		input.Result = &protocol.ExecResult{ExitCode: exitCode}
		subject.Outcome = input.Result
	}
	selected := runner.Select(phase, subject)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	aggregate := runner.Run(ctx, phase, selected, input)
	cancel()
	wouldDeny := a.evaluation.WouldDeny || phase == config.PhasePre && aggregate.Decision == protocol.DecisionDeny
	decision := a.evaluation.Decision
	if wouldDeny {
		if a.config.Policy.Mode == config.ModeEnforce {
			decision = protocol.DecisionDeny
		} else {
			decision = protocol.DecisionAudit
		}
	}
	results := aggregate.Results
	if results == nil {
		results = []protocol.HookResult{}
	}
	if asJSON {
		value := struct {
			Phase     string                `json:"phase"`
			Synthetic bool                  `json:"synthetic"`
			Results   []protocol.HookResult `json:"results"`
			Aggregate struct {
				Decision protocol.Decision `json:"decision"`
				Message  string            `json:"message,omitempty"`
			} `json:"aggregate"`
			Policy policy.Evaluation `json:"policy"`
			Final  struct {
				Decision  protocol.Decision `json:"decision"`
				WouldDeny bool              `json:"wouldDeny"`
			} `json:"final"`
		}{Phase: phase, Synthetic: true, Results: results, Policy: a.evaluation}
		value.Aggregate.Decision, value.Aggregate.Message = aggregate.Decision, aggregate.Message
		value.Final.Decision, value.Final.WouldDeny = decision, wouldDeny
		if code := managementJSON(value, r); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintln(os.Stdout, "Synthetic hook test: original gh command is not executed.")
		if phase == config.PhasePost {
			_, _ = fmt.Fprintf(os.Stdout, "Post hooks receive a synthetic result (exitCode=%d).\n", exitCode)
		}
		for _, result := range results {
			_, _ = fmt.Fprintln(os.Stdout, r.String(managementCompact(result)))
		}
		_, _ = fmt.Fprintf(os.Stdout, "Hooks: %s\nPolicy: %s\nFINAL: %s (mode=%s, would-deny=%t)\n", aggregate.Decision, a.evaluation.Decision, decision, a.config.Policy.Mode, wouldDeny)
		if aggregate.Message != "" {
			_, _ = fmt.Fprintln(os.Stdout, r.String("Message: "+aggregate.Message))
		}
	}
	if expect != "" || expectHooks != "" || expectPolicy != "" {
		code := 0
		for _, check := range []struct {
			name, expected string
			actual         protocol.Decision
		}{
			{"FINAL", expect, decision},
			{"hooks", expectHooks, aggregate.Decision},
			{"policy", expectPolicy, a.evaluation.Decision},
		} {
			if check.expected != "" && check.actual != protocol.Decision(check.expected) {
				_, _ = fmt.Fprintf(os.Stderr, "ghok: expected %s decision %s, actual %s\n", check.name, check.expected, check.actual)
				code = 1
			}
		}
		return code
	}
	if decision == protocol.DecisionDeny {
		return 1
	}
	for _, result := range results {
		switch result.Outcome {
		case protocol.OutcomeError, protocol.OutcomeTimeout, protocol.OutcomeInvalidResponse, protocol.OutcomeCanceled:
			return 1
		}
	}
	return 0
}

func managementCompact(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "<unavailable: " + err.Error() + ">"
	}
	return string(data)
}
