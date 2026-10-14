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
	"github.com/mkusaka/ghok/internal/redact"
	"github.com/spf13/cobra"
)

const managementLong = `Manage ghok, a transparent wrapper for the GitHub CLI (gh) that runs
pre/post hooks and enforces policies on gh commands, gh api REST calls and
GraphQL mutations. When invoked as "gh" (through the shim) the same binary is
the wrapper and passes every argument to the real gh untouched.

Resolution uses local git metadata and GH_REPO/GH_HOST by default; --no-resolve
disables it. Read-only API resolution requires explicit --allow-api.
Explain never executes hooks or the original gh command and exits 0 even on deny.
Exit status: 0 success, 1 failure/denial, 2 usage error.`

// Management runs the management CLI. It never executes the user's gh command.
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
	phase := config.PhasePre
	test := managementCommand("test [flags] -- <gh args>", "Run the hooks matching a gh command (never runs gh itself)", code, func(_ *cobra.Command, args []string) int {
		return managementHooksTest(args, phase, exitCode, testJSON, testOptions)
	})
	test.Long = "Run the hooks that match <gh args> for real, as the wrapper would, but never start\nthe original gh command. Local context is resolved by default; --allow-api permits\nread-only API resolution. Post hooks receive a synthetic result (exit 0 unless --exit-code)."
	test.Example = "  ghok hooks test -- pr merge 123 --admin\n  ghok hooks test --no-resolve -- pr merge 123\n  ghok hooks test --phase post --exit-code 1 -- pr merge 123 -R org/repo"
	test.Args = managementInvocationArgs
	test.Flags().BoolVar(&testJSON, "json", false, "print JSON")
	test.Flags().StringVar(&phase, "phase", config.PhasePre, "hook phase: pre or post")
	test.Flags().IntVar(&exitCode, "exit-code", 0, "post phase: synthetic gh exit code (selects match.result/exitCode hooks)")
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

	configCommands := managementGroup("config", "Create, validate and locate ghok configuration", code)
	var file string
	validate := managementCommand("validate", "Validate the effective configuration (all errors at once)", code, func(_ *cobra.Command, _ []string) int {
		return managementConfig("validate", file)
	})
	validate.Flags().StringVar(&file, "file", "", "global configuration file to validate")
	path := managementCommand("path", "Print the global configuration path", code, func(_ *cobra.Command, _ []string) int {
		return managementConfig("path", "")
	})
	var project bool
	schema := managementCommand("schema", "Print the JSON Schema for config.yaml (or a project .ghok.yaml)", code, func(_ *cobra.Command, _ []string) int {
		_, _ = os.Stdout.Write(config.SchemaJSON(project))
		return 0
	})
	schema.Flags().BoolVar(&project, "project", false, "print the schema for a project .ghok.yaml instead of config.yaml")
	scope := config.ScopeProject
	initCmd := managementCommand("init", "Create a commented config file with a JSON Schema reference", code, func(_ *cobra.Command, _ []string) int {
		return managementConfigInit(scope)
	})
	initCmd.Example = "  ghok config init                # <repo root>/.ghok.yaml\n  ghok config init --scope user   # the global config.yaml"
	initCmd.Flags().StringVar(&scope, "scope", config.ScopeProject, "project: <repo root>/.ghok.yaml; user: the global config.yaml")
	configCommands.AddCommand(initCmd, validate, path, schema)
	root.AddCommand(shim, doctor, versionCmd, hookCommands, configCommands)
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
	fmt.Fprintln(os.Stderr, "ghok:", redact.New(environment()).String(message))
	fmt.Fprintln(os.Stderr, "Run 'ghok help' for usage.")
	return 2
}

func managementFailure(err error) int {
	fmt.Fprintln(os.Stderr, "ghok:", redact.New(environment()).String(err.Error()))
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
	_, notes, err := config.LoadEffective(path, cwd, env)
	for _, note := range notes {
		_, _ = fmt.Fprintln(os.Stdout, r.String(note))
	}
	if err != nil {
		return managementFailure(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Configuration valid")
	return 0
}

func managementDoctor(asJSON bool, version string) int {
	env := environment()
	self, err := os.Executable()
	if err != nil {
		return managementFailure(err)
	}
	c, notes, _, loadErr := managementLoad(env)
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
		cfgCheck.Detail += "; " + loadErr.Error()
		cfgCheck.Fix = "Run ghok config validate and fix the reported configuration errors."
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
	if c != nil {
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
			_, _ = fmt.Fprintf(os.Stdout, "%s %s: %s\n", check.Status, check.Name, check.Detail)
			if check.Fix != "" {
				_, _ = fmt.Fprintln(os.Stdout, "  Fix:", check.Fix)
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
		_, _ = fmt.Fprintf(os.Stdout, "It is ignored until trusted. To trust it, add to %s:\n  trust:\n    projects:\n      - %s\n", config.DefaultPath(env), path)
	}
	return 0
}
