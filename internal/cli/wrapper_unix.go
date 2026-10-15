//go:build unix

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
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
		_, _ = fmt.Fprintln(os.Stdout, "ghok-probe")
		return 0
	}
	started := time.Now()
	// Snapshot before any Notify call changes the observable dispositions.
	var helperSignals []os.Signal
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			helperSignals = append(helperSignals, sig)
		}
	}
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
		fmt.Fprintf(os.Stderr, "ghok: %s.\n", redactor.String(err.Error()))
		return 127
	}
	childEnv, _, err := install.NextDepth(os.Environ())
	if err != nil {
		return fatal(err, 1)
	}
	opts := process.Options{Path: real, Args: append([]string{os.Args[0]}, args...), Env: childEnv}
	if configErr != nil {
		// An invalid effective config cannot be trusted, including its log path.
		cfg = &config.Config{Dir: config.Dir(env)}
		cfg.ApplyDefaults()
	}
	if configErr == nil && cfg.Logging.Enabled != nil && !*cfg.Logging.Enabled && len(cfg.Hooks) == 0 && len(cfg.Policy.Rules) == 0 && cfg.Policy.Mode == config.ModeObserve {
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	eventID := logging.NewInvocationID()
	record := logging.Record{InvocationID: eventID, Time: started.UTC(), Mode: cfg.Policy.Mode, Argv: args, Decision: "allow"}
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
	if configErr != nil {
		if warnInvalidConfig(config.Dir(env), configFailurePath(config.DefaultPath(env), notes), configErr, started) {
			warning := strings.Join(strings.Fields(redactor.String(configErr.Error())), " ")
			fmt.Fprintf(os.Stderr, "ghok: invalid config (%s); running gh without hooks — run 'ghok config validate'\n", warning)
		}
		record.Decision = "passthrough-invalid-config"
		record.Diagnostics = []string{"invalid config: " + configErr.Error()}
		logRecord()
		closeLog()
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	if env["GHOK_NESTED"] == "1" {
		record.Decision = "passthrough-nested"
		logRecord()
		err = process.Exec(opts)
		return fatal(err, process.StartErrorExitCode(err))
	}
	inv := invocation(cfg, args, env, cwd, model.StdinData{Status: model.StdinNotCaptured})
	result := normalize.Normalize(inv)
	spoolNeeded, stdinSkipped := shouldSpool(cfg, result, env)
	if spoolNeeded {
		spool, spoolErr := process.SpoolStdin(os.Stdin, cfg.Runtime.StdinMaxBytes, cfg.Runtime.StdinTimeout.D())
		if spool == nil {
			return fatal(spoolErr, 1)
		} // Replay could not preserve consumed bytes.
		defer func() { _ = spool.Close() }()
		opts.Stdin, inv.Stdin = spool, spool.Data()
		result = normalize.Normalize(inv)
	}
	fillTextSizes(&result, cwd)
	runner := &hooks.Runner{Hooks: cfg.Hooks, BaseDir: cfg.Dir, Concurrency: cfg.Runtime.HookConcurrency, Env: childEnv, RealGh: real, Redact: redactor.String}
	subject := policy.Subject{Result: result, Argv: result.Source.Argv, Token: policy.TokenKind(env), Env: env}
	var resolved *model.ResolvedContext
	localNeeded := needsLocal(cfg, subject)
	ctx, stopHelpers := watchHelpers(helperSignals, len(cfg.Hooks) > 0 || cfg.Policy.Mode == config.ModeEnforce && localNeeded)
	if localNeeded {
		resolver := &resolve.Resolver{Env: env, Cwd: cwd, RealGh: real, Timeout: cfg.Runtime.ResolveTimeout.D()}
		local := resolver.Local()
		resolved = &local
		record.Diagnostics = append(record.Diagnostics, resolver.Fill(ctx, &result, nil, false)...)
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
				record.Diagnostics = append(record.Diagnostics, resolver.Fill(ctx, &one, need, true)...)
				result.Operations[i] = one.Operations[0]
			}
			resolver.Fill(ctx, &result, nil, false)
			subject.Result = result
		}
	}
	evaluation := policy.Evaluate(cfg.Policy, subject)
	input := hookInput(args, cwd, real, result, resolved, evaluation, eventID)
	record.Classification, record.Operations, record.Errors, record.Alias = result.Classification, result.Operations, result.Errors, result.Source.Alias
	record.Source = &result.Source
	if stdinSkipped {
		record.Diagnostics = append(record.Diagnostics, "stdin not analyzed (nothing could act on it)")
	}
	record.Policy = input.Policy
	pre := runner.Run(ctx, config.PhasePre, runner.Select(config.PhasePre, subject), redactor.Input(input))
	post := runner.Select(config.PhasePost, subject)
	// All helper children are reaped before relinquishing signal ownership.
	caught := stopHelpers()
	record.Hooks = pre.Results
	if caught != 0 {
		record.Decision = "canceled"
		record.Exec = execInfo(process.Result{ExitCode: -1, Signal: caught})
		logRecord()
		closeLog()
		if opts.Stdin != nil {
			_ = opts.Stdin.Close()
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
	if stdinSkipped && len(evaluation.Rules) == 0 && pre.Decision != protocol.DecisionDeny {
		// This is deliberate passthrough, not an audit of unavailable input.
		// Keep the unknown operation and original analysis for transparency.
		record.Decision = "allow"
		logPolicy := *record.Policy
		logPolicy.Decision = protocol.DecisionAllow
		record.Policy = &logPolicy
	}
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
	ctx, stopHelpers = watchHelpers(helperSignals, len(post) > 0)
	if execution.StartErr != nil {
		fatal(execution.StartErr, process.StartErrorExitCode(execution.StartErr))
	}
	if execution.WaitErr != nil {
		debug(execution.WaitErr)
	}
	input.Result = execInfo(execution)
	// Candidates were selected before gh ran; apply match.result/exitCode now.
	subject.Outcome = input.Result
	post = runner.Select(config.PhasePost, subject)
	aggregate := runner.Run(ctx, config.PhasePost, post, redactor.Input(input))
	if caught := stopHelpers(); caught != 0 {
		record.Diagnostics = append(record.Diagnostics, "post-hooks interrupted by "+signalName(caught))
	}
	record.Hooks = append(record.Hooks, aggregate.Results...)
	record.Exec = input.Result
	logRecord()
	closeLog()
	execution.Exit()
	return 0
}

// API body-dependent matches remain conservative. Native text provenance uses
// the existing flag parse, including bundled shorthands and interactive flows.
func shouldSpool(c *config.Config, result model.Result, env map[string]string) (spool, skipped bool) {
	argv := result.Source.Argv
	api := len(argv) > 0 && argv[0] == "api"
	s := policy.Subject{Result: result, Argv: argv, Token: policy.TokenKind(env), Env: env}
	if api {
		req, err := apiargs.Parse(argv[1:])
		if err != nil || !apiargs.NeedsStdin(req) {
			return false, false
		}
		level := model.LevelL2
		if apiargs.IsGraphQL(req) {
			level = model.LevelL3
		}
		s.Result = model.NewUnknown(level, model.UnknownInputUnavailable, "stdin not captured")
	} else {
		needed := false
		for _, op := range result.Operations {
			if op.Texts != nil {
				for _, text := range [3]*model.Text{op.Texts.Title, op.Texts.Body, op.Texts.Notes} {
					needed = needed || text != nil && text.Source == "stdin"
				}
			}
		}
		if !needed {
			return false, false
		}
	}
	couldMatch := func(m config.Match) bool {
		if api {
			m.Operations, m.Effects = nil, nil
		}
		yes, _ := policy.Matches(m, s)
		if !yes {
			yes, _ = policy.MatchMaybe(m, s)
		}
		return yes
	}
	if api && c.Policy.Mode == config.ModeEnforce {
		if c.Policy.UnknownMutation == config.UnknownDeny ||
			len(c.Policy.ProtectedRepositories) > 0 && c.Policy.ProtectedUnknown == config.UnknownDeny {
			return true, false
		}
		for _, rule := range c.Policy.Rules {
			if rule.Decision == string(protocol.DecisionDeny) && couldMatch(rule.Match) {
				return true, false
			}
		}
	}
	for _, h := range c.Hooks {
		if h.Phase == config.PhasePre && (h.Enabled == nil || *h.Enabled) && couldMatch(h.Match) {
			return true, false
		}
	}
	return false, true
}

// Native files contain the text itself, not a JSON envelope. Streams stopped at
// the limit have no known total; regular stdin files retain their original offset.
func fillTextSizes(result *model.Result, cwd string) {
	if result.Classification.Level != model.LevelL1 {
		return
	}
	for _, op := range result.Operations {
		if op.Texts == nil {
			continue
		}
		for _, text := range [3]*model.Text{op.Texts.Title, op.Texts.Body, op.Texts.Notes} {
			if text == nil || !text.Truncated {
				continue
			}
			switch text.Source {
			case "file":
				path := text.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(cwd, path)
				}
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					text.Bytes = info.Size()
				}
			case "stdin":
				info, err := os.Stdin.Stat()
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				if offset, err := os.Stdin.Seek(0, io.SeekCurrent); err == nil && offset <= info.Size() {
					text.Bytes = info.Size() - offset
				}
			}
		}
	}
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

// watchHelpers owns signals only outside Exec/Run. The returned stop function
// must be called after helper cleanup and joins the watcher before hand-off.
func watchHelpers(watched []os.Signal, needed bool) (context.Context, func() syscall.Signal) {
	if !needed || len(watched) == 0 {
		return context.Background(), func() syscall.Signal { return 0 }
	}
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 3)
	if len(watched) > 0 {
		signal.Notify(signals, watched...)
	}
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
	return ctx, func() syscall.Signal {
		signal.Stop(signals)
		close(done)
		<-joined
		cancel()
		return caught
	}
}
