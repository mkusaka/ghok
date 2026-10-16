//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
	"github.com/mkusaka/ghok/internal/normalize"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
	"github.com/mkusaka/ghok/internal/schema"
	"github.com/spf13/cobra"
)

const managementLong = `Manage ghok, a transparent wrapper for the GitHub CLI (gh) that runs
pre/post hooks and enforces policies on gh commands, gh api REST calls and
GraphQL mutations. The shim ("gh") and "ghok exec -- <gh args>" use the same
wrapper pipeline; trusted transforms may rewrite the request before policy/hooks.

Resolution uses local git metadata and GH_REPO/GH_HOST by default; --no-resolve
disables it. Read-only API resolution requires explicit --allow-api.
Explain runs trusted transforms, but never hooks or the original gh command, and exits 0 even on policy deny.
Management exit status: 0 success, 1 failure/denial, 2 usage error.
Exec preserves gh status/signals; denials use 77 (content) or 78 (hook/config error).`

// Management runs the CLI, including explicit wrapper execution through exec.
func Management(args []string, version, commit string) int {
	code := 0
	root := managementRoot(&code, version, commit)
	root.SetArgs(args)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	if err := root.Execute(); err != nil {
		return managementUsageError(err.Error())
	}
	return code
}

func managementRoot(code *int, version, commit string) *cobra.Command {
	root := managementGroup("ghok", "Manage the ghok gh wrapper: shim, config, hooks, diagnostics", code)
	root.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Help() } // bare "ghok": help, exit 0
	// Parents are runnable only to validate arguments; don't advertise them
	// as "ghok config [flags]" usage lines.
	root.SetUsageTemplate(strings.Replace(root.UsageTemplate(), "{{if .Runnable}}", "{{if and .Runnable (not .HasAvailableSubCommands)}}", 1))
	root.Long = managementLong
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.DisableSuggestions = true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return err })
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root && len(cmd.Flags().Args()) != 0 {
			*code = managementUsageError("help does not accept arguments")
			return
		}
		defaultHelp(cmd, args)
	})

	execCmd := managementCommand("exec -- <gh args>", "Run gh through the wrapper pipeline without installing a shim", code, func(_ *cobra.Command, args []string) int {
		return wrapper(args, "gh", "exec")
	})
	execCmd.Args = managementInvocationArgs
	execCmd.Long = `Run exactly the same discovery, recursion checks, policy, hooks, execution and
logging as the gh shim. Intended for tests, CI and scripts without a shim on PATH.
This is not a policy bypass. The real gh receives argv[0]="gh"; only trusted transforms rewrite args.
Exit status and signals match the shim: content denial 77, hook/config error denial
78, internal error 1, missing real gh 127; otherwise gh's original status/signal.`
	root.AddCommand(execCmd)

	shim := managementGroup("shim", "Install or remove the gh shim (gh -> ghok)", code)
	defaultBin := filepath.Join(environment()["HOME"], ".local", "bin")
	for _, name := range []string{"install", "uninstall"} {
		bin := defaultBin
		short := "Create the gh shim that routes gh through ghok (shell rc files are not modified)"
		if name == "uninstall" {
			short = "Remove the gh shim created by ghok (other files are never touched)"
		}
		cmd := managementCommand(name, short, code, func(cmd *cobra.Command, _ []string) int {
			return managementShim(cmd.Name(), bin)
		})
		cmd.Flags().StringVar(&bin, "bin-dir", bin, "directory containing the gh shim")
		shim.AddCommand(cmd)
	}

	var doctorJSON bool
	doctor := managementCommand("doctor", "Check installation, PATH precedence, configuration and hook permissions", code, func(_ *cobra.Command, _ []string) int {
		return managementDoctor(doctorJSON, version)
	})
	doctor.Flags().BoolVar(&doctorJSON, "json", false, "print diagnostic checks as JSON")
	versionCmd := managementCommand("version", "Show version, commit, Go version and platform", code, func(_ *cobra.Command, _ []string) int {
		return managementVersion(version, commit)
	})

	hookCommands := managementGroup("hooks", "List configured hooks or run them against a gh command", code)
	var listJSON bool
	list := managementCommand("list", "List configured hooks", code, func(_ *cobra.Command, _ []string) int {
		return managementHooksList(listJSON)
	})
	list.Flags().BoolVar(&listJSON, "json", false, "print JSON")
	var testJSON bool
	var testOptions managementInspectOptions
	var exitCode int
	var expect, expectHooks, expectPolicy string
	phase := config.PhasePre
	test := managementCommand("test [flags] -- <gh args>", "Run the hooks matching a gh command (never runs gh itself)", code, func(cmd *cobra.Command, args []string) int {
		if cmd.Flags().Changed("hook") && testOptions.hookID == "" {
			return managementUsageError("--hook requires a nonempty hook ID")
		}
		if cmd.Flags().Changed("expect") {
			switch protocol.Decision(expect) {
			case protocol.DecisionAllow, protocol.DecisionDeny, protocol.DecisionAbstain, protocol.DecisionAudit:
			default:
				return managementUsageError("--expect must be allow, deny, abstain or audit")
			}
		}
		if cmd.Flags().Changed("expect-hooks") {
			switch protocol.Decision(expectHooks) {
			case protocol.DecisionAllow, protocol.DecisionDeny, protocol.DecisionAbstain:
			default:
				return managementUsageError("--expect-hooks must be allow, deny or abstain")
			}
		}
		if cmd.Flags().Changed("expect-policy") {
			switch protocol.Decision(expectPolicy) {
			case protocol.DecisionAllow, protocol.DecisionDeny, protocol.DecisionAudit:
			default:
				return managementUsageError("--expect-policy must be allow, audit or deny")
			}
		}
		return managementHooksTest(args, phase, exitCode, testJSON, testOptions, expect, expectHooks, expectPolicy)
	})
	test.Long = `Run trusted transforms and matching trusted hooks for real, but never start the original gh command.
Local context is resolved by default; --allow-api permits read-only API resolution.
Post hooks receive a synthetic result (exit 0 unless --exit-code).
--print-input runs trusted transforms, then prints one hook input JSON envelope without running hooks or gh,
including API resolution; it cannot be combined with --allow-api.
It masks credentials for display by default;
--show-secrets shows the bytes the hook receives, still honoring redactInput.
Use --hook ID to preview a matching configured hook; omit it for a generic input.

Print the hook aggregate (allow/deny/abstain), policy decision (allow/audit/deny),
and FINAL decision separately. No matching hooks or all abstaining means hooks=abstain.
FINAL combines policy and pre-hook denial with the configured mode; observe-mode
would-deny is audit, not deny.

--expect compares FINAL (allow/deny/abstain/audit), preserving its existing semantics:
no hook denial and policy=allow means FINAL=allow, even when hooks=abstain.
--expect-hooks compares the hook aggregate; --expect-policy compares policy alone.
With any expectations, exit 0 only if all match, even on denial/hook error;
otherwise exit 1 with the failed expectation named.
Without expectations, exit 1 on FINAL deny or hook execution error (nonzero exit,
spawn failure, timeout, invalid response, cancellation), otherwise 0.
Configuration/analysis failures exit 1; usage errors exit 2.`
	test.Example = "  ghok hooks test -- pr merge 123 --admin\n  ghok hooks test --no-resolve -- pr merge 123\n  ghok hooks test --phase post --exit-code 1 -- pr merge 123 -R org/repo"
	test.Args = managementInvocationArgs
	test.Flags().BoolVar(&testJSON, "json", false, "print JSON")
	test.Flags().BoolVar(&testOptions.printInput, "print-input", false, "print one hook input JSON envelope without executing hooks or gh")
	test.Flags().StringVar(&testOptions.hookID, "hook", "", "preview the input for a matching configured hook (requires --print-input)")
	test.Flags().BoolVar(&testOptions.showSecrets, "show-secrets", false, "show the bytes the hook receives in --print-input output; still honors redactInput")
	test.Flags().StringVar(&phase, "phase", config.PhasePre, "hook phase: pre or post")
	test.Flags().IntVar(&exitCode, "exit-code", 0, "post phase: synthetic gh exit code (selects match.result/exitCode hooks)")
	test.Flags().StringVar(&expect, "expect", "", "expect the FINAL decision: allow, deny, abstain or audit (observe would-deny is audit)")
	test.Flags().StringVar(&expectHooks, "expect-hooks", "", "expect the hook aggregate: allow, deny or abstain")
	test.Flags().StringVar(&expectPolicy, "expect-policy", "", "expect the policy decision: allow, audit or deny")
	managementResolutionFlags(test, &testOptions)
	hookCommands.AddCommand(list, test)

	for _, name := range []string{"normalize", "explain"} {
		var options managementInspectOptions
		short := "Print how ghok classifies a gh command (redacted JSON)"
		if name == "explain" {
			short = "Explain classification, policy decision and matching hooks for a gh command"
		}
		cmd := managementCommand(name+" [flags] -- <gh args>", short, code, func(cmd *cobra.Command, args []string) int {
			return managementInspect(cmd.Name(), args, options)
		})
		cmd.Long = short + ".\nLocal context is resolved by default without network access. Use --no-resolve\nto skip resolution, or --allow-api to permit read-only API resolution."
		cmd.Example = "  ghok " + name + " -- pr merge 123 --squash\n  ghok " + name + " --no-resolve -- pr merge 123\n  ghok " + name + " --allow-api -- pr merge 123\n  ghok " + name + " -- api -X PUT repos/org/repo/pulls/123/merge"
		cmd.Args = managementInvocationArgs
		managementResolutionFlags(cmd, &options)
		if name == "normalize" {
			cmd.Flags().Bool("json", true, "print the normalization result as JSON (the only output format)")
		} else {
			cmd.Flags().BoolVar(&options.showSecrets, "show-secrets", false, "show secrets in explain output; never affects hook payload redaction")
		}
		root.AddCommand(cmd)
	}

	shellInit := managementGroup("shell-init", "Print a gh shell function that runs gh through ghok exec (for eval in a shell rc file)", code)
	for _, name := range []string{"zsh", "bash", "fish"} {
		shellInit.AddCommand(managementCommand(name, "Print the "+name+" gh function", code, func(cmd *cobra.Command, _ []string) int {
			fn := `function gh { ghok exec -- "$@"; }`
			if cmd.Name() == "fish" {
				fn = "function gh; ghok exec -- $argv; end"
			}
			_, _ = fmt.Fprintln(os.Stdout, shellInitCaveat+fn)
			return 0
		}))
	}

	configCommands := managementGroup("config", "Create, validate and locate ghok configuration", code)
	var file string
	validate := managementCommand("validate", "Validate the effective configuration (all errors at once)", code, func(_ *cobra.Command, _ []string) int {
		return managementConfig("validate", file)
	})
	validate.Flags().StringVar(&file, "file", "", "global configuration file to validate")
	path := managementCommand("path", "Print the global configuration path", code, func(_ *cobra.Command, _ []string) int {
		return managementConfig("path", "")
	})
	schemaCommands := managementGroup("schema", "Print JSON Schemas for configuration, hooks and transforms", code)
	for _, name := range []string{"config", "project-config", "hook-input", "hook-response", "transform-response"} {
		cmd := managementCommand(name, "Print the "+name+" JSON Schema", code, func(cmd *cobra.Command, _ []string) int {
			body, err := schema.JSON(cmd.Name())
			if err == nil {
				_, err = os.Stdout.Write(body)
			}
			if err != nil {
				return managementFailure(err)
			}
			return 0
		})
		schemaCommands.AddCommand(cmd)
	}
	scope := config.ScopeProject
	initCmd := managementCommand("init", "Create a commented config file with a JSON Schema reference", code, func(_ *cobra.Command, _ []string) int {
		return managementConfigInit(scope)
	})
	initCmd.Example = "  ghok config init                # <repo root>/.ghok.yaml\n  ghok config init --scope user   # the global config.yaml"
	initCmd.Flags().StringVar(&scope, "scope", config.ScopeProject, "project: <repo root>/.ghok.yaml; user: the global config.yaml")
	for _, name := range []string{"trust", "untrust"} {
		cmd := managementCommand(name+" [PATH]", "Manage trust for project hooks in the current .ghok.yaml or PATH", code, func(cmd *cobra.Command, args []string) int {
			path := ""
			if len(args) != 0 {
				path = args[0]
			}
			list, _ := cmd.Flags().GetBool("list")
			all, _ := cmd.Flags().GetBool("all")
			if list || all {
				return managementConfigTrustStore(all)
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			noDiff, _ := cmd.Flags().GetBool("no-diff")
			showSecrets, _ := cmd.Flags().GetBool("show-secrets")
			hookIDs, _ := cmd.Flags().GetStringArray("hook")
			return managementConfigTrust(cmd.Name(), path, dryRun, noDiff, showSecrets, hookIDs)
		})
		flag := "list"
		if name == "untrust" {
			flag = "all"
		}
		cmd.Flags().Bool(flag, false, "manage all stored project trust without PATH")
		if name == "trust" {
			cmd.Flags().Bool("dry-run", false, "show changes and what would be trusted without writing anything")
			cmd.Flags().Bool("no-diff", false, "omit the stored-content diff")
			cmd.Flags().Bool("show-secrets", false, "show the diff unredacted (credentials are masked by default)")
			cmd.Flags().StringArray("hook", nil, "trust only this project hook ID (repeatable); retain other hooks' trust")
			cmd.MarkFlagsMutuallyExclusive("list", "hook")
			cmd.MarkFlagsMutuallyExclusive("list", "dry-run")
			cmd.MarkFlagsMutuallyExclusive("list", "no-diff")
			cmd.MarkFlagsMutuallyExclusive("no-diff", "show-secrets")
		}
		cmd.Args = func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return err
			}
			enabled, _ := cmd.Flags().GetBool(flag)
			if enabled && len(args) != 0 {
				return fmt.Errorf("--%s cannot be combined with PATH", flag)
			}
			return nil
		}
		configCommands.AddCommand(cmd)
	}
	configCommands.AddCommand(initCmd, validate, path)
	root.AddCommand(shim, shellInit, doctor, versionCmd, hookCommands, configCommands, schemaCommands)
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: "Show help for a command",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			cmd, rest, err := root.Find(args)
			if err != nil {
				return err
			}
			if err := managementCommandArgs(cmd, rest); err != nil {
				return err
			}
			return cmd.Help()
		},
	})
	return root
}

func managementResolutionFlags(cmd *cobra.Command, options *managementInspectOptions) {
	cmd.Flags().BoolVar(&options.noResolve, "no-resolve", false, "disable local target resolution")
	cmd.Flags().Bool("resolve", false, "deprecated: no-op; local target resolution is the default")
	cmd.Flags().BoolVar(&options.allowAPI, "allow-api", false, "allow read-only API resolution (incompatible with --no-resolve)")
}

func managementCommand(use, short string, code *int, run func(*cobra.Command, []string) int) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  managementCommandArgs,
		Run: func(cmd *cobra.Command, args []string) {
			*code = run(cmd, args)
		},
	}
}

func managementCommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if cmd.HasSubCommands() {
		if cmd.Parent() == nil {
			return fmt.Errorf("unknown command %s", args[0])
		}
		return fmt.Errorf("unknown %s command %s", cmd.Name(), args[0])
	}
	return fmt.Errorf("unexpected argument %s", args[0])
}

func managementInvocationArgs(cmd *cobra.Command, args []string) error {
	// No gh argument may precede --; pflag leaves everything after it untouched.
	if cmd.ArgsLenAtDash() != 0 || len(args) == 0 {
		return errors.New("provide gh arguments after --")
	}
	return nil
}

// managementGroup is a parent command. Invoked without a subcommand it shows
// its help and exits 2; unknown subcommands are usage errors.
func managementGroup(use, short string, code *int) *cobra.Command {
	return managementCommand(use, short, code, func(cmd *cobra.Command, _ []string) int {
		_ = cmd.Help()
		return 2
	})
}

// shellInitCaveat is printed so it stays next to the function in rc files.
const shellInitCaveat = `# ghok: gh typed in this shell runs through 'ghok exec'. gh started by scripts,
# programs or agents that skip shell rc files is not covered; 'ghok shim install' is.
`

func managementVersion(version, commit string) int {
	if version == "" {
		version = "dev"
	}
	_, _ = fmt.Fprintf(os.Stdout, "ghok %s\n", version)
	if commit != "" {
		_, _ = fmt.Fprintf(os.Stdout, "commit %s\n", commit)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return 0
}

func managementUsageError(message string) int {
	fmt.Fprintln(os.Stderr, "ghok:", terminalText(redact.New(environment()).String(message)))
	fmt.Fprintln(os.Stderr, "Run 'ghok help' for usage.")
	return 2
}

func managementFailure(err error) int {
	fmt.Fprintln(os.Stderr, "ghok:", terminalText(redact.New(environment()).String(err.Error())))
	return 1
}

func managementJSON(v any, r *redact.Redactor) int {
	if r != nil {
		data, err := json.Marshal(v)
		if err != nil {
			return managementFailure(err)
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return managementFailure(err)
		}
		v = r.Value(value)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return managementFailure(err)
	}
	return 0
}

func managementLoad(env map[string]string) (*config.Config, []string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, "", err
	}
	c, notes, err := config.LoadEffective(config.DefaultPath(env), cwd, env)
	return c, notes, cwd, err
}

func managementShim(command, bin string) int {
	if bin == "" {
		return managementUsageError("--bin-dir must not be empty")
	}
	env := environment()
	self, err := os.Executable()
	if err != nil {
		return managementFailure(err)
	}
	state := filepath.Join(config.Dir(env), "state")
	r := redact.New(env)
	if command == "uninstall" {
		removed, err := install.Disable(install.DisableOptions{BinDir: bin, Self: self, StateDir: state})
		if err != nil {
			return managementFailure(err)
		}
		if len(removed) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "No ghok shim to remove; other files are unchanged.")
		}
		for _, path := range removed {
			_, _ = fmt.Fprintln(os.Stdout, r.String("Removed "+path))
		}
		return 0
	}
	result, err := install.Enable(install.EnableOptions{BinDir: bin, Self: self, StateDir: state, PathEnv: env["PATH"]})
	if err != nil {
		return managementFailure(err)
	}
	if err := install.ProbeShim(result.ShimPath, os.Environ()); err != nil {
		return managementFailure(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, r.String("Installed ghok shim: "+result.ShimPath))
	_, _ = fmt.Fprintln(os.Stdout, r.String("Real gh: "+result.RealGh))
	_, _ = fmt.Fprintln(os.Stdout, r.String("PATH: "+result.Precedence))
	if result.FixHint != "" {
		_, _ = fmt.Fprintln(os.Stdout, r.String("Fix: "+result.FixHint))
	}
	_, _ = fmt.Fprintln(os.Stdout, "Shim probe passed. Shell rc files were not modified.")
	_, _ = fmt.Fprintln(os.Stdout, "repos without a ghok config are unaffected: gh is exec'd directly")
	return 0
}

func managementConfig(command, file string) int {
	env := environment()
	selected := config.DefaultPath(env)
	if file != "" {
		selected = file
	}
	path, err := filepath.Abs(selected)
	if err != nil {
		return managementFailure(err)
	}
	r := redact.New(env)
	if command == "path" {
		_, _ = fmt.Fprintln(os.Stdout, r.String(path))
		return 0
	}
	cwd, err := os.Getwd()
	if err != nil {
		return managementFailure(err)
	}
	c, notes, err := config.LoadEffective(path, cwd, env)
	notes = append(notes, operationPatternWarnings(c)...)
	for _, note := range notes {
		_, _ = fmt.Fprintln(os.Stdout, terminalText(r.String(note)))
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stdout, configFailureBehavior(err, env))
		return managementFailure(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Configuration valid")
	return 0
}

func operationPatternWarnings(c *config.Config) []string {
	if c == nil {
		return nil
	}
	var warnings []string
	check := func(location string, m config.Match) {
		for i, pattern := range m.Operations {
			if !normalize.KnownActionPattern(pattern) {
				warnings = append(warnings, fmt.Sprintf("warning: %s.match.operation[%d]: pattern %q matches no known action", location, i, pattern))
			}
		}
	}
	for i, rule := range c.Policy.Rules {
		check(fmt.Sprintf("policy.rules[%d] (%s)", i, rule.ID), rule.Match)
	}
	for i, hook := range c.Hooks {
		check(fmt.Sprintf("hooks[%d] (%s)", i, hook.ID), hook.Match)
	}
	for i, hook := range c.IgnoredHooks {
		check(fmt.Sprintf("ignoredHooks[%d] (%s)", i, hook.ID), hook.Match)
	}
	return warnings
}

func configFailureBehavior(err error, env map[string]string) string {
	if config.FailClosed(err, env) {
		if env["GHOK_FAIL_CLOSED"] == "1" {
			return "Invalid config behavior: fail-closed (GHOK_FAIL_CLOSED=1); gh will not run."
		}
		return "Invalid config behavior: fail-closed (user policy.mode: enforce); gh will not run."
	}
	return "Invalid config behavior: fail-open (observe or undetermined user mode); gh runs without hooks with a throttled warning."
}

func managementDoctor(asJSON bool, version string) int {
	env := environment()
	self, err := os.Executable()
	if err != nil {
		return managementFailure(err)
	}
	c, notes, cwd, loadErr := managementLoad(env)
	noConfig := loadErr == nil && !config.HasConfig(cwd, env)
	notes = append(notes, operationPatternWarnings(c)...)
	override := env["GHOK_REAL_GH"]
	if override == "" && c != nil {
		override = c.Runtime.RealGh
	}
	checks := install.Checks(install.DoctorOptions{Self: self, Version: version, PathEnv: env["PATH"], Override: override, BinDir: filepath.Join(env["HOME"], ".local", "bin"), StateDir: filepath.Join(config.Dir(env), "state"), Env: os.Environ(), Stdin: os.Stdin})
	cfgCheck := install.Check{Name: "config", Status: install.StatusOK}
	for _, note := range notes {
		if strings.HasPrefix(note, "Global config:") || strings.HasPrefix(note, "Project config:") {
			if cfgCheck.Detail != "" {
				cfgCheck.Detail += "; "
			}
			cfgCheck.Detail += note
		}
	}
	if loadErr != nil {
		cfgCheck.Status = install.StatusFail
		cfgCheck.Detail += "; " + loadErr.Error() + "; " + configFailureBehavior(loadErr, env)
		cfgCheck.Fix = "Run ghok config validate and fix the reported configuration errors."
	} else if noConfig {
		cfgCheck.Status = install.StatusInfo
		cfgCheck.Detail = "no config: the wrapper is a pure passthrough and logs nothing; run 'ghok config init --scope user' to enable logging/auditing"
	}
	checks = append(checks, cfgCheck)
	for _, note := range notes {
		if strings.HasPrefix(note, "Global config:") || strings.HasPrefix(note, "Project config:") {
			continue
		}
		status := install.StatusWarn
		if strings.Contains(note, "group/world-writable") {
			status = install.StatusFail
		}
		checks = append(checks, install.Check{Name: "config-note", Status: status, Detail: note})
	}
	if info, err := os.Stat(config.DefaultPath(env)); err == nil && info.Mode().Perm()&0022 != 0 {
		checks = append(checks, install.Check{Name: "config-permissions", Status: install.StatusFail, Detail: "Global configuration is group/world writable", Fix: "Remove group/world write permission from the configuration."})
	}
	if c != nil && !noConfig {
		for _, h := range c.Hooks {
			checks = append(checks, managementHookCheck(c, h, env))
		}
		if c.Logging.Enabled != nil && *c.Logging.Enabled {
			checks = append(checks, managementLogCheck(c.Logging.Path))
		} else {
			checks = append(checks, install.Check{Name: "log-writable", Status: install.StatusInfo, Detail: "Logging disabled"})
		}
	}
	r := redact.New(env)
	failed := false
	for i := range checks {
		failed = failed || checks[i].Status == install.StatusFail
		checks[i].Name = r.String(checks[i].Name)
		checks[i].Detail = r.String(checks[i].Detail)
		checks[i].Fix = r.String(checks[i].Fix)
	}
	if asJSON {
		if code := managementJSON(checks, nil); code != 0 {
			return code
		}
	} else {
		for _, check := range checks {
			_, _ = fmt.Fprintln(os.Stdout, terminalText(fmt.Sprintf("%s %s: %s", check.Status, check.Name, check.Detail)))
			if check.Fix != "" {
				_, _ = fmt.Fprintln(os.Stdout, terminalText("  Fix: "+check.Fix))
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

func managementHookCheck(c *config.Config, h config.Hook, env map[string]string) install.Check {
	check := install.Check{Name: "hook:" + h.ID, Status: install.StatusOK}
	if h.Enabled != nil && !*h.Enabled {
		check.Status, check.Detail = install.StatusInfo, "Disabled"
		return check
	}
	name := c.HookCommandPath(h)
	path := name
	if !strings.ContainsRune(name, filepath.Separator) {
		search := env["PATH"]
		if value, ok := h.Env["PATH"]; ok {
			search = value
		}
		path = ""
		for _, dir := range filepath.SplitList(search) {
			if dir == "" {
				dir = "."
			}
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				path = candidate
				break
			}
		}
	}
	info, err := os.Stat(path)
	if path == "" || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		check.Status = install.StatusFail
		if h.Optional {
			check.Status = install.StatusWarn
		}
		check.Detail = "Executable missing or not executable: " + name
		check.Fix = "Install the hook executable or correct its command and permissions."
		return check
	}
	check.Detail = path
	if info.Mode().Perm()&0022 != 0 {
		check.Status = install.StatusFail
		check.Detail += " is group/world writable"
		check.Fix = "Remove group/world write permission from the trusted hook executable."
	}
	return check
}

func managementLogCheck(path string) install.Check {
	check := install.Check{Name: "log-writable", Status: install.StatusOK, Detail: path}
	fail := func(err error) install.Check {
		check.Status, check.Detail = install.StatusFail, path+": "+err.Error()
		check.Fix = "Choose a writable logging path with a nonsymlink directory and regular log/lock files."
		return check
	}
	for _, file := range []string{path, path + ".lock"} {
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("%s is not a regular nonsymlink file", file))
		}
		if info.Mode().Perm()&0222 == 0 {
			return fail(fmt.Errorf("%s has no write permissions", file))
		}
		if err := syscall.Access(file, 2); err != nil {
			return fail(err)
		}
	}
	dir := filepath.Dir(path)
	for {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			parent := filepath.Dir(dir)
			if parent == dir {
				return fail(err)
			}
			dir = parent
			continue
		}
		if err != nil {
			return fail(err)
		}
		// The final logging directory must not be a symlink. Ancestors may be.
		if dir == filepath.Dir(path) && info.Mode()&os.ModeSymlink != 0 {
			return fail(fmt.Errorf("logging directory is a symlink"))
		}
		info, err = os.Stat(dir)
		if err != nil {
			return fail(err)
		}
		if !info.IsDir() || info.Mode().Perm()&0222 == 0 || info.Mode().Perm()&0111 == 0 {
			return fail(fmt.Errorf("directory %s is not writable/searchable", dir))
		}
		if err := syscall.Access(dir, 3); err != nil {
			return fail(err)
		}
		return check
	}
}

func managementConfigInit(scope string) int {
	env := environment()
	cwd, err := os.Getwd()
	if err != nil {
		return managementFailure(err)
	}
	path, err := config.Init(scope, cwd, env)
	if err != nil {
		return managementFailure(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Created "+path)
	if scope == config.ScopeProject {
		_, _ = fmt.Fprintln(os.Stdout, "Project rules apply automatically. If you add hooks, inspect them and run ghok config trust to enable them.")
	}
	return 0
}
