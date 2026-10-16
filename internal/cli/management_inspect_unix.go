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
	config           *config.Config
	notes            []string
	cwd              string
	env              map[string]string
	invocation       model.Invocation
	result           model.Result
	context          *model.ResolvedContext
	evaluation       policy.Evaluation
	diagnostics      []string
	real             string
	transformIDs     []string
	transformResults []protocol.HookResult
	cleanup          func()
}

func managementAnalyze(args []string, resolveTargets, allowAPI, transform bool) (*managementAnalysis, error) {
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
	a.cleanup = func() {}
	if transform && len(c.Transforms) > 0 {
		a.real, _ = realGh(c, env)
		helperEnv, release, err := nestedEnvironment(env)
		if err != nil {
			return nil, err
		}
		r := redact.New(env)
		runner := &hooks.Runner{BaseDir: c.Dir, Env: append(os.Environ(), "GHOK_NESTED_TOKEN="+helperEnv["GHOK_NESTED_TOKEN"]), RealGh: a.real, Redact: r.String, Source: "test"}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		applied := runTransforms(ctx, c, runner, inv, a.result, a.real, logging.NewInvocationID(), "shim")
		cancel()
		release()
		if applied.denial != nil {
			applied.cleanup()
			return nil, fmt.Errorf("denied by transform %s (error: %s): %s", applied.denial.ID, applied.denial.ErrorKind, applied.denial.Message)
		}
		a.invocation, a.result, a.cleanup = applied.invocation, applied.result, applied.cleanup
		a.transformIDs, a.transformResults = applied.ids, applied.results
	}
	if resolveTargets {
		helperEnv := env
		if allowAPI {
			a.real, err = realGh(c, env)
			if err != nil {
				a.cleanup()
				return nil, err
			}
			var cleanup func()
			helperEnv, cleanup, err = nestedEnvironment(env)
			if err != nil {
				a.cleanup()
				return nil, err
			}
			defer cleanup()
		}
		r := &resolve.Resolver{Env: helperEnv, Cwd: cwd, RealGh: a.real, Timeout: c.Runtime.ResolveTimeout.D()}
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
	a.evaluation = evaluateConfig(c, a.subject(""))
	return a, nil
}

func (a *managementAnalysis) subject(phase string) policy.Subject {
	return policy.Subject{Result: a.result, Argv: a.result.Source.Argv, Context: a.context, Phase: phase, Token: policy.TokenKind(a.env), Env: a.env}
}

type managementInspectOptions struct {
	noResolve   bool
	allowAPI    bool
	showSecrets bool
	printInput  bool
	hookID      string
}

func managementInspect(command string, args []string, options managementInspectOptions) int {
	if options.allowAPI && options.noResolve {
		return managementUsageError("--allow-api cannot be combined with --no-resolve")
	}
	a, err := managementAnalyze(args, !options.noResolve, options.allowAPI, command != "normalize")
	if err != nil {
		return managementFailure(err)
	}
	defer a.cleanup()
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
		Ignored  string `json:"ignored,omitempty"`
		Inactive string `json:"inactive,omitempty"`
		// Wait lists the background hooks this hook joins (wait, wait-all,
		// parallel block end) before it starts.
		Wait []string `json:"wait,omitempty"`
	}
	display := make([]listedHook, 0, len(c.Hooks)+len(c.IgnoredHooks))
	for _, h := range c.Hooks {
		h.Command = r.Argv(h.Command)
		display = append(display, listedHook{Hook: h, Wait: h.Wait})
	}
	for _, h := range c.IgnoredHooks {
		h.Command = r.Argv(h.Command)
		display = append(display, listedHook{Hook: h.Hook, Ignored: h.Reason, Inactive: ignoredHookDescription(h)})
	}
	if asJSON {
		return managementJSON(display, r)
	}
	if len(display) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No hooks configured.")
	}
	for _, h := range display {
		enabled := h.Enabled == nil || *h.Enabled
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(fmt.Sprintf("%s phase=%s enabled=%t background=%t timeout=%s onError=%s optional=%t captureOutput=%t", h.ID, h.Phase, enabled, h.Background, h.Timeout.D(), h.OnError, h.Optional, h.CaptureOutput))))
		if h.Ignored != "" {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("  ignored ("+h.Ignored+")")))
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("  "+h.Inactive)))
		}
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("  source: "+h.Source)))
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("  command: "+managementCompact(h.Command))))
		managementPrintMatch(os.Stdout, "  ", h.Match, r)
		if len(h.DependsOn) != 0 {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("  dependsOn: "+strings.Join(h.DependsOn, ", "))))
		}
		if len(h.Wait) != 0 {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("  waits for: "+strings.Join(h.Wait, ", "))))
		}
	}
	for _, note := range notes {
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("Note: "+note)))
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
	if options.printInput && options.allowAPI {
		return managementUsageError("--allow-api cannot be combined with --print-input")
	}
	if options.hookID != "" && !options.printInput {
		return managementUsageError("--hook requires --print-input")
	}
	a, err := managementAnalyze(args, !options.noResolve, options.allowAPI, true)
	if err != nil {
		return managementFailure(err)
	}
	defer a.cleanup()
	// Discovery supplies hook helpers a real-gh path when available, but a pure
	// hook test remains usable on machines without gh. No original gh is started.
	a.real, _ = realGh(a.config, a.env)
	r := redact.New(a.env)
	runner := hooks.Runner{Hooks: a.config.Hooks, BaseDir: a.config.Dir, Concurrency: a.config.Runtime.HookConcurrency, Env: os.Environ(), RealGh: a.real, Redact: r.String, Source: "test"}
	if exitCode < 0 || exitCode > 255 {
		return managementUsageError("--exit-code must be 0-255")
	}
	subject := a.subject(phase)
	input := hookInput(a.invocation.Argv, a.cwd, a.real, a.result, a.context, a.evaluation, logging.NewInvocationID())
	if phase == config.PhasePost {
		// A synthetic execution result, not a real one; it also drives
		// match.result / match.exitCode selection.
		input.Result = &protocol.ExecResult{ExitCode: exitCode}
		subject.Outcome = input.Result
	}
	selected := runner.Select(phase, subject)
	if options.printInput {
		h := config.Hook{}
		if options.hookID != "" {
			found := false
			for _, selectedHook := range selected {
				if selectedHook.ID == options.hookID {
					h, found = selectedHook, true
					break
				}
			}
			if !found {
				return managementUsageError(fmt.Sprintf("unknown or non-matching hook ID %q for phase %s", options.hookID, phase))
			}
		}
		input = hooks.PrepareInput(phase, h, input, r)
		// stdout stays pure JSON (pipe to jq / save as a fixture); notes go to stderr.
		target := "any matching hook"
		if options.hookID != "" {
			target = "hook " + options.hookID
		}
		fmt.Fprintf(os.Stderr, "ghok: %s input for %s (source: test; hooks and gh not executed; invocation.via shows the real entry point)\n", phase, target)
		if !options.showSecrets {
			masked := r.WithCount(func(display *redact.Redactor) {
				input = display.HookInput(input)
			})
			if masked > 0 {
				fmt.Fprintf(os.Stderr, "%d values masked; use --show-secrets for the bytes the hook receives\n", masked)
			}
		}
		return managementJSON(input, nil)
	}
	if len(selected) > 0 {
		helperEnv, cleanup, err := nestedEnvironment(a.env)
		if err != nil {
			return managementFailure(err)
		}
		defer cleanup()
		runner.Env = append(os.Environ(), "GHOK_NESTED_TOKEN="+helperEnv["GHOK_NESTED_TOKEN"])
	}
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
			Transformed      *managementTransformed `json:"transformed,omitempty"`
			TransformResults []protocol.HookResult  `json:"transformResults,omitempty"`
		}{Phase: phase, Synthetic: true, Results: results, Policy: a.evaluation}
		value.Transformed = a.transformed()
		value.TransformResults = a.transformResults
		value.Aggregate.Decision, value.Aggregate.Message = aggregate.Decision, aggregate.Message
		value.Final.Decision, value.Final.WouldDeny = decision, wouldDeny
		if code := managementJSON(value, r); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintln(os.Stdout, "Synthetic hook test: original gh command is not executed.")
		managementPrintTransformed(os.Stdout, a, false)
		if phase == config.PhasePost {
			_, _ = fmt.Fprintf(os.Stdout, "Post hooks receive a synthetic result (exitCode=%d).\n", exitCode)
		}
		for _, result := range results {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(managementCompact(result))))
		}
		_, _ = fmt.Fprintf(os.Stdout, "Hooks: %s\nPolicy: %s\nFINAL: %s (mode=%s, would-deny=%t)\n", aggregate.Decision, a.evaluation.Decision, decision, a.config.Policy.Mode, wouldDeny)
		for _, reason := range a.evaluation.DenyReasons {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("Reason: "+reason)))
		}
		if aggregate.Message != "" {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String("Message: "+aggregate.Message)))
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
