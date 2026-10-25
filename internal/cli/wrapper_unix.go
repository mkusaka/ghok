//go:build unix

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
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
	return wrapper(args, os.Args[0], "shim")
}

// Version is the ghok version reported under `gh --version`; set by main.
var Version = "dev"

// execGh replaces the process with gh, except for `gh --version` / `gh version`
// on a terminal, which runs gh and then reports ghok below gh's own version.
// Piped output stays byte-identical for scripts and hooks that parse it.
func execGh(opts process.Options, args []string) error {
	if len(args) != 1 || args[0] != "--version" && args[0] != "version" || !install.IsTTY(os.Stdout) {
		return process.Exec(opts)
	}
	result := process.Run(context.Background(), opts)
	if result.StartErr != nil {
		return result.StartErr
	}
	if result.ExitCode == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "ghok version %s (wrapping %s)\n", Version, opts.Path)
	}
	result.Exit()
	return nil
}

func wrapper(args []string, argv0, via string) int {
	if os.Getenv("GHOK_PROBE") == "1" {
		_, _ = fmt.Fprintln(os.Stdout, "ghok-probe")
		return 0
	}
	env := environment()
	var redactor *redact.Redactor
	if via == "exec" {
		install.RecordExecEntry(filepath.Join(config.Dir(env), "state"), time.Now())
	}
	fatal := func(err error, code int) int {
		if redactor == nil {
			redactor = redact.New(env)
		}
		fmt.Fprintln(os.Stderr, "ghok:", redactor.String(err.Error()))
		return code
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fatal(err, 1)
	}
	if !config.HasConfig(cwd, env) {
		real, err := realGh(nil, env)
		if err != nil {
			return fatal(err, 127)
		}
		childEnv, _, err := install.NextDepth(os.Environ())
		if err != nil {
			return fatal(err, 1)
		}
		err = execGh(process.Options{Path: real, Args: append([]string{argv0}, args...), Env: childEnv}, args)
		return fatal(err, process.StartErrorExitCode(err))
	}
	started := time.Now()
	// Snapshot before any Notify call changes the observable dispositions.
	var helperSignals []os.Signal
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			helperSignals = append(helperSignals, sig)
		}
	}
	redactor = redact.New(env)
	cfg, notes, configErr := config.LoadEffective(config.DefaultPath(env), cwd, env)
	failClosed := config.FailClosed(configErr, env)
	var real string
	var childEnv []string
	var opts process.Options
	if !failClosed {
		real, err = realGh(cfg, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ghok: %s.\n", redactor.String(err.Error()))
			return 127
		}
		childEnv, _, err = install.NextDepth(os.Environ())
		if err != nil {
			return fatal(err, 1)
		}
		opts = process.Options{Path: real, Args: append([]string{argv0}, args...), Env: childEnv}
	}
	if configErr != nil {
		// An invalid effective config cannot be trusted, including its log path.
		cfg = &config.Config{Dir: config.Dir(env)}
		cfg.ApplyDefaults()
	}
	nested := configErr == nil && install.ValidNestedToken(config.Dir(env), env)
	warnIgnoredHooks := func() {
		if configErr == nil && !nested {
			if warning := ignoredHooksWarning(cfg, config.Dir(env), started); warning != "" {
				fmt.Fprintln(os.Stderr, "ghok:", strings.Join(strings.Fields(redactor.String(warning)), " "))
			}
		}
	}
	if configErr == nil && cfg.Logging.Enabled != nil && !*cfg.Logging.Enabled && len(cfg.Hooks) == 0 && len(cfg.Transforms) == 0 && len(cfg.IgnoredHooks) == 0 && len(cfg.Policy.Rules) == 0 && cfg.Policy.Mode == config.ModeObserve {
		warnIgnoredHooks()
		err = execGh(opts, args)
		return fatal(err, process.StartErrorExitCode(err))
	}
	eventID := logging.NewInvocationID()
	record := logging.Record{InvocationID: eventID, Time: started.UTC(), Mode: cfg.Policy.Mode, Argv: args, Decision: "allow", Diagnostics: cfg.Diagnostics}
	record.Invocation.Via = via
	var logger *logging.Logger
	var logOpenErr error
	debug := func(err error) {
		if err != nil && env["GHOK_DEBUG"] == "1" {
			fmt.Fprintln(os.Stderr, "ghok:", redactor.String(err.Error()))
		}
	}
	if cfg.Logging.Enabled == nil || *cfg.Logging.Enabled {
		logger, logOpenErr = logging.OpenWithRedactor(cfg.Logging.Path, cfg.Logging.Level, redactor)
	}
	logRecord := func() {
		debug(logOpenErr)
		logOpenErr = nil
		record.DurationMs = time.Since(started).Milliseconds()
		// Config failures are errors, not routine diagnostics: retain them at
		// decision log level even when the requested gh operation is allowed.
		record.Errors = append(record.Errors, cfg.Diagnostics...)
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
	execLogged := func() int {
		warnIgnoredHooks()
		logRecord()
		execStarted := time.Now()
		execErr := execGh(opts, args)
		code := process.StartErrorExitCode(execErr)
		record.Decision = "exec-error"
		record.Exec = execInfo(process.Result{ExitCode: code, StartErr: execErr, Duration: time.Since(execStarted)})
		logRecord()
		return fatal(execErr, code)
	}
	if configErr != nil {
		if failClosed {
			printDenials(os.Stderr, []denial{{Source: "config", Message: configErr.Error() + "\nfail-closed: not running gh — run 'ghok config validate'"}}, redactor.String)
			record.Decision = "deny-invalid-config"
			record.Diagnostics = []string{"invalid config: " + configErr.Error()}
			logRecord()
			return 78
		}
		if warnInvalidConfig(config.Dir(env), configFailurePath(config.DefaultPath(env), notes), configErr, started) {
			warning := strings.Join(strings.Fields(redactor.String(configErr.Error())), " ")
			fmt.Fprintf(os.Stderr, "ghok: invalid config (%s); running gh without hooks — run 'ghok config validate'\n", warning)
		}
		record.Decision = "passthrough-invalid-config"
		record.Diagnostics = []string{"invalid config: " + configErr.Error()}
		return execLogged()
	}
	if nested {
		record.Decision = "passthrough-nested"
		return execLogged()
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
	subject := policy.Subject{Result: result, Argv: result.Source.Argv, Token: policy.TokenKind(env), Env: env}
	var resolved *model.ResolvedContext
	localNeeded := needsLocal(cfg, subject)
	helperEnv, hookEnv := env, childEnv
	releaseNested := func() {}
	if len(cfg.Hooks) > 0 || len(cfg.Transforms) > 0 || localNeeded && cfg.Runtime.ResolveAPI != "never" {
		helperEnv, releaseNested, err = nestedEnvironment(env)
		if err != nil {
			return fatal(err, 1)
		}
		hookEnv = append(childEnv, "GHOK_NESTED_TOKEN="+helperEnv["GHOK_NESTED_TOKEN"])
	}
	defer releaseNested()
	runner := &hooks.Runner{Hooks: cfg.Hooks, BaseDir: cfg.Dir, Concurrency: cfg.Runtime.HookConcurrency, Env: hookEnv, RealGh: real, Redact: redactor.String, Redactor: redactor, Source: via}
	ctx, stopHelpers := watchHelpers(helperSignals, len(cfg.Hooks) > 0 || len(cfg.Transforms) > 0 || localNeeded && cfg.Runtime.ResolveAPI != "never")
	transformed := runTransforms(ctx, cfg, runner, inv, result, real, eventID, via)
	defer transformed.cleanup()
	if transformed.denial != nil {
		caught := stopHelpers()
		record.Hooks = transformed.results
		record.Warnings = resultWarnings("transform", transformed.results)
		record.Decision = "deny"
		if caught != 0 {
			record.Decision = "canceled"
			record.Exec = execInfo(process.Result{ExitCode: -1, Signal: caught})
			logRecord()
			closeLog()
			transformed.cleanup()
			releaseNested()
			process.Result{ExitCode: -1, Signal: caught}.Exit()
		}
		printDenials(os.Stderr, []denial{*transformed.denial}, redactor.String)
		logRecord()
		return 78
	}
	if len(transformed.ids) > 0 {
		record.Transformed = transformedLog(result, transformed.result, transformed.invocation.Argv, transformed.ids, transformed.envChanged(), transformed.envBy)
		opts.Args = append([]string{argv0}, transformed.invocation.Argv...)
		if !bytes.Equal(inv.Stdin.Data, transformed.invocation.Stdin.Data) {
			replay, err := process.SpoolBytes(transformed.invocation.Stdin.Data)
			if err != nil {
				stopHelpers()
				return fatal(err, 1)
			}
			defer func() { _ = replay.Close() }()
			opts.Stdin = replay
		}
	}
	inv, result = transformed.invocation, transformed.result
	if len(transformed.envBy) > 0 {
		// Rebuild from the edited, unincremented invocation environment.
		opts.Env, _, err = install.NextDepth(environmentList(inv.Env))
		if err != nil {
			stopHelpers()
			return fatal(err, 1)
		}
		token := helperEnv["GHOK_NESTED_TOKEN"]
		helperEnv = make(map[string]string, len(inv.Env)+2)
		for name, value := range inv.Env {
			helperEnv[name] = value
		}
		if token != "" {
			helperEnv["GHOK_NESTED"], helperEnv["GHOK_NESTED_TOKEN"] = "1", token
		}
	}
	subject.Env, subject.Token = inv.Env, policy.TokenKind(inv.Env)
	subject.Result, subject.Argv = result, result.Source.Argv
	localNeeded = needsLocal(cfg, subject)
	if localNeeded {
		resolver := &resolve.Resolver{Env: helperEnv, Cwd: cwd, RealGh: real, Timeout: cfg.Runtime.ResolveTimeout.D()}
		local := resolver.Local()
		resolved = &local
		record.Diagnostics = append(record.Diagnostics, resolver.Fill(ctx, &result, nil, false)...)
		subject.Result, subject.Context = result, resolved
		if cfg.Runtime.ResolveAPI != "never" {
			// Fill only the operation whose unresolved repository/host could
			// affect this invocation's rules or enabled pre/post hooks.
			for i, op := range result.Operations {
				need := resolutionNeeds(cfg, subject, i)
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
	evaluation := evaluateConfig(cfg, subject)
	input := hookInput(inv, real, result, resolved, evaluation, eventID)
	input.Invocation.Via = via
	input.Invocation.EnvChanged = transformed.envChanged()
	record.Classification, record.Operations, record.Errors, record.Alias = result.Classification, result.Operations, result.Errors, result.Source.Alias
	record.Source = &result.Source
	if stdinSkipped {
		record.Diagnostics = append(record.Diagnostics, "stdin not analyzed (nothing could act on it)")
	}
	record.Policy = input.Policy
	pre := runner.Run(ctx, config.PhasePre, runner.Select(config.PhasePre, subject), input)
	post := runner.Select(config.PhasePost, subject)
	// All helper children are reaped before relinquishing signal ownership.
	caught := stopHelpers()
	record.Hooks = append(transformed.results, pre.Results...)
	record.Warnings = append(record.Warnings, evaluation.Warnings...)
	transformWarnings := resultWarnings("transform", transformed.results)
	record.Warnings = append(record.Warnings, transformWarnings...)
	record.Warnings = append(record.Warnings, resultWarnings("hook", pre.Results)...)
	if caught != 0 {
		record.Decision = "canceled"
		record.Exec = execInfo(process.Result{ExitCode: -1, Signal: caught})
		logRecord()
		closeLog()
		if opts.Stdin != nil {
			_ = opts.Stdin.Close()
		}
		releaseNested()
		transformed.cleanup()
		process.Result{ExitCode: -1, Signal: caught}.Exit()
	}
	decision := decide(cfg.Policy.Mode, evaluation, pre, stdinSkipped)
	record.Decision = decision.Decision
	if decision.Decision == "deny" {
		attachHookStderr(decision.Denials, pre, cfg.Hooks)
		printDenials(os.Stderr, decision.Denials, redactor.String)
		logRecord()
		return decision.ExitCode
	}
	printWarnings(os.Stderr, evaluation.Warnings, redactor.String)
	printWarnings(os.Stderr, transformWarnings, redactor.String)
	printHookWarnings(os.Stderr, pre, cfg.Hooks, redactor.String)
	if decision.RelabelPolicy {
		logPolicy := *record.Policy
		logPolicy.Decision = protocol.DecisionAllow
		record.Policy = &logPolicy
	}
	if len(post) == 0 && len(inv.TemporaryFiles) == 0 && (opts.Stdin == nil || !opts.Stdin.RequiresSupervision()) {
		releaseNested()
		transformed.cleanup()
		return execLogged()
	}
	for _, h := range post {
		opts.Capture = opts.Capture || h.CaptureOutput
	}
	opts.CaptureLimit = cfg.Runtime.CaptureLimit
	warnIgnoredHooks()
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
	runner.Env = opts.Env
	if token := helperEnv["GHOK_NESTED_TOKEN"]; token != "" {
		runner.Env = append(append([]string(nil), opts.Env...), "GHOK_NESTED_TOKEN="+token)
	}
	aggregate := runner.Run(ctx, config.PhasePost, post, input)
	if caught := stopHelpers(); caught != 0 {
		record.Diagnostics = append(record.Diagnostics, "post-hooks interrupted by "+signalName(caught))
	}
	record.Hooks = append(record.Hooks, aggregate.Results...)
	postWarnings := resultWarnings("hook", aggregate.Results)
	record.Warnings = append(record.Warnings, postWarnings...)
	record.Exec = input.Result
	logRecord()
	closeLog()
	releaseNested()
	transformed.cleanup()
	printWarnings(os.Stderr, postWarnings, redactor.String)
	execution.Exit()
	return 0
}

// API body-dependent matches remain conservative. Native text provenance uses
// the existing flag parse, including bundled shorthands and interactive flows.
func shouldSpool(c *config.Config, result model.Result, env map[string]string) (spool, skipped bool) {
	argv := result.Source.Argv
	needed, which := normalize.NeedsStdin(argv)
	if !needed {
		return false, false
	}
	api := which == "api"
	s := policy.Subject{Result: result, Argv: argv, Token: policy.TokenKind(env), Env: env}
	if api {
		req, _ := apiargs.Parse(argv[1:])
		level := model.LevelL2
		if apiargs.IsGraphQL(req) {
			level = model.LevelL3
		}
		s.Result = model.NewUnknown(level, model.UnknownInputUnavailable, "stdin not captured")
	}
	// unless is ignored here: excluding by an unknown api request's operations
	// could skip the full pipeline, so the fast path stays conservative.
	couldMatch := func(m config.Match) bool {
		if api {
			m.Operations, m.Effects, m.Parameters = nil, nil, nil
		}
		yes, _ := policy.Matches(m, nil, s)
		if !yes {
			yes, _ = policy.MatchMaybe(m, nil, s)
		}
		return yes
	}
	if api && c.Policy.Mode == config.ModeEnforce {
		if c.Policy.UnknownInput == config.UnknownDeny || c.Policy.UnknownMutation == config.UnknownDeny ||
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
	for _, t := range c.Transforms {
		if couldMatch(t.Match) {
			return true, false
		}
	}
	for _, h := range c.IgnoredHooks {
		if h.GateBlocked && couldMatch(h.Match) {
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
	if c.Policy.Mode == config.ModeEnforce && len(c.Policy.ProtectedRepositories) > 0 {
		for _, op := range s.Result.Operations {
			if consideredOperation(op) && op.Effect != model.EffectRead && unresolvedTarget(s.Result.Classification.Level, op, "target.repository") {
				return true
			}
		}
	}
	// Resolving also matters when only an unless entry is unresolved for a
	// selected operation: the resolved target could exclude it.
	relevant := func(m config.Match, unless []config.Match) bool {
		if maybe, _ := policy.MatchMaybe(m, unless, s); maybe {
			return true
		}
		_, indexes := policy.Matches(m, unless, s)
		for _, i := range indexes {
			if repo, host := policy.UnlessMaybe(unless, s, i); repo || host {
				return true
			}
		}
		return false
	}
	for _, rule := range c.Policy.Rules {
		if relevant(rule.Match, rule.Unless) {
			return true
		}
	}
	for _, h := range c.Hooks {
		if (h.Enabled == nil || *h.Enabled) && relevant(h.Match, h.Unless) {
			return true
		}
	}
	for _, h := range c.IgnoredHooks {
		if h.GateBlocked && relevant(h.Match, h.Unless) {
			return true
		}
	}
	return false
}

func consideredOperation(op model.Operation) bool {
	return op.Execution != model.ExecutionSkipped && op.Execution != model.ExecutionNotSelected
}

func resolutionNeeds(c *config.Config, s policy.Subject, index int) []string {
	op := s.Result.Operations[index]
	if !consideredOperation(op) {
		return nil
	}
	repo, host := false, false
	if c.Policy.Mode == config.ModeEnforce && len(c.Policy.ProtectedRepositories) > 0 && op.Effect != model.EffectRead && unresolvedTarget(s.Result.Classification.Level, op, "target.repository") {
		repo = true
	}
	match := func(m config.Match, unless []config.Match) {
		if _, indexes := policy.MatchMaybe(m, unless, s); slices.Contains(indexes, index) {
			repo = repo || len(m.Repositories) > 0 && unresolvedTarget(s.Result.Classification.Level, op, "target.repository")
			host = host || len(m.Hosts) > 0 && unresolvedTarget(s.Result.Classification.Level, op, "target.host")
		} else if _, indexes = policy.Matches(m, unless, s); !slices.Contains(indexes, index) {
			return
		}
		unlessRepo, unlessHost := policy.UnlessMaybe(unless, s, index)
		repo = repo || unlessRepo && unresolvedTarget(s.Result.Classification.Level, op, "target.repository")
		host = host || unlessHost && unresolvedTarget(s.Result.Classification.Level, op, "target.host")
	}
	for _, rule := range c.Policy.Rules {
		match(rule.Match, rule.Unless)
	}
	for _, h := range c.Hooks {
		if h.Enabled == nil || *h.Enabled {
			match(h.Match, h.Unless)
		}
	}
	for _, h := range c.IgnoredHooks {
		if h.GateBlocked {
			match(h.Match, h.Unless)
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

func unresolvedTarget(level model.Level, op model.Operation, field string) bool {
	if field == "target.repository" && op.Target.Repository == "" {
		return !policy.RepositoryLess(level, op)
	}
	if field == "target.host" && op.Target.Host == "" {
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
