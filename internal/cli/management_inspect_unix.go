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
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
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
	if apiargs.NeedsStdinArgv(expandedArgv(inv)) {
		if install.IsTTY(os.Stdin) {
			inv.Stdin.Status = model.StdinTTY
		} else {
			spool, err := process.SpoolStdin(os.Stdin, c.Runtime.StdinMaxBytes, c.Runtime.StdinTimeout.D())
			if spool != nil {
				defer spool.Close()
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
	return policy.Subject{Result: a.result, Argv: a.result.Source.Argv, Context: a.context, Phase: phase}
}

func managementInspect(command string, args []string) int {
	fs := managementFlags(command, "ghok "+command+" [options] -- <gh args>")
	resolveTargets := fs.Bool("resolve", false, "resolve targets from local git metadata")
	allowAPI := fs.Bool("allow-api", false, "allow read-only API resolution (requires --resolve)")
	showSecrets := false
	if command == "normalize" {
		fs.Bool("json", true, "print the normalization result as JSON (the only output format)")
	} else {
		fs.BoolVar(&showSecrets, "show-secrets", false, "show secrets in explain output; never affects hook payload redaction")
	}
	if code := managementParse(fs, args, true); code >= 0 {
		return code
	}
	if *allowAPI && !*resolveTargets {
		return managementUsageError("--allow-api requires --resolve")
	}
	a, err := managementAnalyze(fs.Args(), *resolveTargets, *allowAPI)
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
	return managementExplain(a, showSecrets)
}

func managementHooks(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stdout, "Usage: ghok hooks list [--json]\n       ghok hooks test [--phase pre|post] [--json] -- <gh args>")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if args[0] != "list" && args[0] != "test" {
		return managementUsageError("unknown hooks command " + args[0])
	}
	command := args[0]
	usage := "ghok hooks list [--json]"
	if command == "test" {
		usage = "ghok hooks test [--phase pre|post] [--json] -- <gh args>"
	}
	fs := managementFlags("hooks "+command, usage)
	asJSON := fs.Bool("json", false, "print JSON")
	phase := config.PhasePre
	if command == "test" {
		fs.StringVar(&phase, "phase", config.PhasePre, "hook phase: pre or post")
	}
	if code := managementParse(fs, args[1:], command == "test"); code >= 0 {
		return code
	}
	if phase != config.PhasePre && phase != config.PhasePost {
		return managementUsageError("--phase must be pre or post")
	}
	if command == "list" {
		env := environment()
		c, notes, _, err := managementLoad(env)
		if err != nil {
			return managementFailure(err)
		}
		r := redact.New(env)
		// Command flags need contextual redaction in addition to JSON key redaction.
		display := make([]config.Hook, len(c.Hooks))
		copy(display, c.Hooks)
		for i := range display {
			display[i].Command = r.Argv(display[i].Command)
		}
		if *asJSON {
			return managementJSON(display, r)
		}
		if len(display) == 0 {
			fmt.Fprintln(os.Stdout, "No hooks configured.")
		}
		for _, h := range display {
			enabled := h.Enabled == nil || *h.Enabled
			fmt.Fprintf(os.Stdout, "%s\n", r.String(fmt.Sprintf("%s phase=%s enabled=%t priority=%d timeout=%s onError=%s optional=%t captureOutput=%t", h.ID, h.Phase, enabled, h.Priority, h.Timeout.D(), h.OnError, h.Optional, h.CaptureOutput)))
			fmt.Fprintln(os.Stdout, r.String("  command: "+managementCompact(h.Command)))
			managementPrintMatch(os.Stdout, "  ", h.Match, r)
			if len(h.DependsOn) != 0 {
				fmt.Fprintln(os.Stdout, r.String("  dependsOn: "+strings.Join(h.DependsOn, ", ")))
			}
		}
		for _, note := range notes {
			fmt.Fprintln(os.Stdout, r.String("Note: "+note))
		}
		return 0
	}
	a, err := managementAnalyze(fs.Args(), true, false)
	if err != nil {
		return managementFailure(err)
	}
	// Discovery supplies hook helpers a real-gh path when available, but a pure
	// hook test remains usable on machines without gh. No original gh is started.
	a.real, _ = realGh(a.config, a.env)
	r := redact.New(a.env)
	runner := hooks.Runner{Hooks: a.config.Hooks, BaseDir: a.config.Dir, Concurrency: a.config.Runtime.HookConcurrency, Env: os.Environ(), RealGh: a.real, Redact: r.String}
	selected := runner.Select(phase, a.subject(phase))
	input := hookInput(fs.Args(), a.cwd, a.real, a.result, a.context, a.evaluation, logging.NewInvocationID())
	if phase == config.PhasePost {
		// This is explicitly a synthetic successful execution, not a real result.
		input.Result = &protocol.ExecResult{ExitCode: 0}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	aggregate := runner.Run(ctx, phase, selected, r.Input(input))
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
	if *asJSON {
		value := struct {
			Phase     string                `json:"phase"`
			Synthetic bool                  `json:"synthetic"`
			Results   []protocol.HookResult `json:"results"`
			Aggregate struct {
				Decision  protocol.Decision `json:"decision"`
				WouldDeny bool              `json:"wouldDeny"`
				Message   string            `json:"message,omitempty"`
			} `json:"aggregate"`
			Policy policy.Evaluation `json:"policy"`
		}{Phase: phase, Synthetic: true, Results: results, Policy: a.evaluation}
		value.Aggregate.Decision, value.Aggregate.WouldDeny, value.Aggregate.Message = decision, wouldDeny, aggregate.Message
		if code := managementJSON(value, r); code != 0 {
			return code
		}
	} else {
		fmt.Fprintln(os.Stdout, "Synthetic hook test: original gh command is not executed.")
		if phase == config.PhasePost {
			fmt.Fprintln(os.Stdout, "Post hooks receive a synthetic successful result (exitCode=0).")
		}
		for _, result := range results {
			fmt.Fprintln(os.Stdout, r.String(managementCompact(result)))
		}
		fmt.Fprintf(os.Stdout, "Aggregate: %s (mode=%s, would-deny=%t)\n", decision, a.config.Policy.Mode, wouldDeny)
		if aggregate.Message != "" {
			fmt.Fprintln(os.Stdout, r.String("Message: "+aggregate.Message))
		}
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
