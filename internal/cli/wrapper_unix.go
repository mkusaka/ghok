//go:build unix

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

// Wrapper executes args as real gh, enforcing configured pre-decisions. It
// returns only on denial/error; successful Exec replaces the process, and Run
// terminates through Result.Exit to preserve the child's exact wait status.
func Wrapper(args []string) int {
	if os.Getenv("GHOK_PROBE") == "1" {
		fmt.Fprintln(os.Stdout, "ghok-probe")
		return 0
	}
	started := time.Now()
	env := environment()
	redactor := redact.New(env)
	fatal := func(err error, code int) int {
		fmt.Fprintln(os.Stderr, "ghok:", redactor.String(err.Error()))
		return code
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fatal(err, 1)
	}
	cfg, notes, configErr := config.LoadEffective(config.DefaultPath(env), cwd, env)
	real, err := realGh(cfg, env)
	if err != nil {
		return fatal(err, 127)
	}
	childEnv, _, err := install.NextDepth(os.Environ())
	if err != nil {
		return fatal(err, 1)
	}
	opts := process.Options{Path: real, Args: append([]string{os.Args[0]}, args...), Env: childEnv}
	if configErr != nil {
		warning := strings.Join(strings.Fields(redactor.String(configErr.Error())), " ")
		fmt.Fprintf(os.Stderr, "ghok: invalid config (%s); running gh without hooks — run 'ghok config validate'\n", warning)
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	if cfg.Logging.Enabled != nil && !*cfg.Logging.Enabled && len(cfg.Hooks) == 0 && len(cfg.Policy.Rules) == 0 && cfg.Policy.Mode == config.ModeObserve {
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	eventID := logging.NewInvocationID()
	record := logging.Record{InvocationID: eventID, Time: started.UTC(), Mode: cfg.Policy.Mode, Argv: args, Diagnostics: notes, Decision: "allow"}
	var logger *logging.Logger
	debug := func(err error) {
		if err != nil && env["GHOK_DEBUG"] == "1" {
			fmt.Fprintln(os.Stderr, "ghok:", redactor.String(err.Error()))
		}
	}
	if cfg.Logging.Enabled == nil || *cfg.Logging.Enabled {
		logger, err = logging.OpenWithRedactor(cfg.Logging.Path, cfg.Logging.Level, redactor)
		debug(err)
	}
	logRecord := func() {
		record.DurationMs = time.Since(started).Milliseconds()
		if logger != nil {
			debug(logger.Write(record))
		}
	}
	closeLog := func() {
		if logger != nil {
			debug(logger.Close())
		}
	}
	defer closeLog()
	if env["GHOK_NESTED"] == "1" {
		record.Decision = "passthrough-nested"
		logRecord()
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	inv := invocation(cfg, args, env, cwd, model.StdinData{Status: model.StdinNotCaptured})
	if apiargs.NeedsStdinArgv(expandedArgv(inv)) {
		spool, spoolErr := process.SpoolStdin(os.Stdin, cfg.Runtime.StdinMaxBytes, cfg.Runtime.StdinTimeout.D())
		if spool == nil {
			return fatal(spoolErr, 1)
		} // Replay could not preserve consumed bytes.
		defer spool.Close()
		opts.Stdin, inv.Stdin = spool, spool.Data()
	}
	result := normalize.Normalize(inv)
	runner := &hooks.Runner{Hooks: cfg.Hooks, BaseDir: cfg.Dir, Concurrency: cfg.Runtime.HookConcurrency, Env: childEnv, RealGh: real, Redact: redactor.String}
	subject := policy.Subject{Result: result, Argv: result.Source.Argv}
	var resolved *model.ResolvedContext
	if needsLocal(cfg, subject) {
		resolver := &resolve.Resolver{Env: env, Cwd: cwd, RealGh: real, Timeout: cfg.Runtime.ResolveTimeout.D()}
		local := resolver.Local()
		resolved = &local
		record.Diagnostics = append(record.Diagnostics, resolver.Fill(context.Background(), &result, nil, false)...)
		subject.Result, subject.Context = result, resolved
		if cfg.Policy.Mode == config.ModeEnforce {
			// Request only fields relevant to an enforcing match, and only on that
			// operation; Fill's need list otherwise applies to every operation.
			for i, op := range result.Operations {
				need := enforcementNeeds(cfg, subject, i)
				if len(need) == 0 {
					continue
				}
				one := model.Result{SchemaVersion: result.SchemaVersion, Classification: result.Classification, Source: result.Source, Operations: []model.Operation{op}}
				record.Diagnostics = append(record.Diagnostics, resolver.Fill(context.Background(), &one, need, true)...)
				result.Operations[i] = one.Operations[0]
			}
			resolver.Fill(context.Background(), &result, nil, false)
			subject.Result = result
		}
	}
	evaluation := policy.Evaluate(cfg.Policy, subject)
	input := hookInput(args, cwd, real, result, resolved, evaluation, eventID)
	record.Classification, record.Operations, record.Errors, record.Alias = result.Classification, result.Operations, result.Errors, result.Source.Alias
	record.Policy = input.Policy
	pre, caught := runPre(runner, runner.Select(config.PhasePre, subject), redactor.Input(input))
	record.Hooks = pre.Results
	if caught != 0 {
		record.Decision = "canceled"
		record.Exec = execInfo(process.Result{ExitCode: -1, Signal: caught})
		logRecord()
		closeLog()
		if opts.Stdin != nil {
			opts.Stdin.Close()
		}
		process.Result{ExitCode: -1, Signal: caught}.Exit()
	}
	wouldDeny := evaluation.WouldDeny || evaluation.Decision == protocol.DecisionDeny || pre.Decision == protocol.DecisionDeny
	if wouldDeny {
		if cfg.Policy.Mode == config.ModeEnforce {
			record.Decision = "deny"
			reason := strings.Join(evaluation.DenyReasons, "; ")
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
					if reason != "" {
						reason += "; "
					}
					reason += hookReason
				}
				if pre.Message != "" && pre.Message != hookReason {
					if reason != "" {
						reason += "; "
					}
					reason += pre.Message
				}
			}
			if reason == "" {
				reason = "pre-execution policy"
			}
			prefix := "ghok: denied:"
			if evaluation.Decision == protocol.DecisionDeny {
				prefix = "ghok: denied by policy:"
			}
			fmt.Fprintln(os.Stderr, prefix, redactor.String(reason))
			logRecord()
			return 1
		}
		record.Decision = "would-deny"
	} else if evaluation.Decision == protocol.DecisionAudit {
		record.Decision = "audit"
	}
	post := runner.Select(config.PhasePost, subject)
	if len(post) == 0 && (opts.Stdin == nil || !opts.Stdin.RequiresSupervision()) {
		logRecord()
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	for _, h := range post {
		opts.Capture = opts.Capture || h.CaptureOutput
	}
	opts.CaptureLimit = cfg.Runtime.CaptureLimit
	execution := process.Run(context.Background(), opts)
	if execution.StartErr != nil {
		fatal(execution.StartErr, process.StartErrorExitCode(execution.StartErr))
	}
	if execution.WaitErr != nil {
		debug(execution.WaitErr)
	}
	input.Result = execInfo(execution)
	aggregate := runner.Run(context.Background(), config.PhasePost, post, redactor.Input(input))
	record.Hooks = append(record.Hooks, aggregate.Results...)
	record.Exec = input.Result
	logRecord()
	closeLog()
	execution.Exit()
	return 0
}

func needsLocal(c *config.Config, s policy.Subject) bool {
	if len(c.Policy.ProtectedRepositories) > 0 {
		for _, op := range s.Result.Operations {
			if consideredOperation(op) && op.Effect != model.EffectRead && unresolvedTarget(op, "target.repository") {
				return true
			}
		}
	}
	relevant := func(m config.Match) bool {
		maybe, _ := policy.MatchMaybe(m, s)
		return maybe
	}
	for _, rule := range c.Policy.Rules {
		if relevant(rule.Match) {
			return true
		}
	}
	for _, h := range c.Hooks {
		if (h.Enabled == nil || *h.Enabled) && relevant(h.Match) {
			return true
		}
	}
	return false
}

func consideredOperation(op model.Operation) bool {
	return op.Execution != model.ExecutionSkipped && op.Execution != model.ExecutionNotSelected
}

func enforcementNeeds(c *config.Config, s policy.Subject, index int) []string {
	op := s.Result.Operations[index]
	if !consideredOperation(op) {
		return nil
	}
	repo, host := false, false
	if len(c.Policy.ProtectedRepositories) > 0 && op.Effect != model.EffectRead && unresolvedTarget(op, "target.repository") {
		repo = true
	}
	match := func(m config.Match) {
		maybe, indexes := policy.MatchMaybe(m, s)
		if !maybe {
			return
		}
		for _, i := range indexes {
			if i == index {
				repo = repo || len(m.Repositories) > 0 && unresolvedTarget(op, "target.repository")
				host = host || len(m.Hosts) > 0 && unresolvedTarget(op, "target.host")
			}
		}
	}
	for _, rule := range c.Policy.Rules {
		if rule.Decision == string(protocol.DecisionDeny) {
			match(rule.Match)
		}
	}
	for _, h := range c.Hooks {
		if h.Phase == config.PhasePre && (h.Enabled == nil || *h.Enabled) {
			match(h.Match)
		}
	}
	var need []string
	if repo {
		need = append(need, "target.repository")
	}
	if host {
		need = append(need, "target.host")
	}
	return need
}

func unresolvedTarget(op model.Operation, field string) bool {
	if field == "target.repository" && op.Target.Repository == "" || field == "target.host" && op.Target.Host == "" {
		return true
	}
	for _, u := range op.Unresolved {
		if u.Field == field {
			return true
		}
	}
	return false
}

// runPre owns INT/TERM only while hooks run. Stop and join the signal watcher
// before Exec/Run takes signal ownership; cancellation waits for hook groups.
func runPre(r *hooks.Runner, selected []config.Hook, input protocol.Input) (hooks.Aggregate, syscall.Signal) {
	if len(selected) == 0 {
		return hooks.Aggregate{Decision: protocol.DecisionAbstain}, 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 2)
	watched := []os.Signal{syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGINT) {
		watched = append(watched, syscall.SIGINT)
	}
	signal.Notify(signals, watched...)
	done, joined := make(chan struct{}), make(chan struct{})
	var caught syscall.Signal
	go func() {
		defer close(joined)
		select {
		case sig := <-signals:
			caught = sig.(syscall.Signal)
			cancel()
		case <-done:
			select {
			case sig := <-signals:
				caught = sig.(syscall.Signal)
				cancel()
			default:
			}
		}
	}()
	aggregate := r.Run(ctx, config.PhasePre, selected, input)
	signal.Stop(signals)
	close(done)
	<-joined
	return aggregate, caught
}
