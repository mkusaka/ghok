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
	"github.com/spf13/pflag"
)

const managementUsage = `Usage: ghok <command> [options]

Commands:
  shim install [--bin-dir DIR] Install the gh shim (default ~/.local/bin)
  shim uninstall [--bin-dir DIR]
                              Remove only ghok's shim
  doctor [--json]              Check installation, configuration and permissions
  version                     Show version, commit, Go version and platform
  hooks list [--json]          List configured hooks
  hooks test [--phase pre|post] [--json] -- <gh args>
                              Run hooks, never the original gh command
  normalize [--json] [--resolve] [--allow-api] -- <gh args>
                              Print redacted normalization JSON
  explain [--show-secrets] [--resolve] [--allow-api] -- <gh args>
                              Explain operations, policy and hook matching
  config validate [--file PATH] Validate effective configuration
  config path                 Print the global configuration path
  config schema [--project]   Print the JSON Schema for config.yaml (or .ghok.yaml)
  help [command]              Show help

Resolution is local unless --allow-api is explicitly supplied with --resolve.
Explain never executes hooks or the original gh command and exits 0 even on deny.
Exit status: 0 success, 1 failure/denial, 2 usage error.
`

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
	root := managementCommand("ghok", managementUsage, code, func(cmd *cobra.Command, _ []string) int {
		_ = cmd.Help()
		return 0
	})
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.DisableSuggestions = true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(managementFlagError)

	shim := managementCommand("shim", "Usage: ghok shim <install|uninstall> [--bin-dir DIR]\n", code, managementGroup)
	defaultBin := filepath.Join(environment()["HOME"], ".local", "bin")
	for _, name := range []string{"install", "uninstall"} {
		bin := defaultBin
		cmd := managementCommand(name, "Usage: ghok shim "+name+" [--bin-dir DIR]\n", code, func(cmd *cobra.Command, _ []string) int {
			return managementShim(cmd.Name(), bin)
		})
		cmd.Flags().StringVar(&bin, "bin-dir", bin, "directory containing the gh shim")
		shim.AddCommand(cmd)
	}

	var doctorJSON bool
	doctor := managementCommand("doctor", "Usage: ghok doctor [--json]\n", code, func(_ *cobra.Command, _ []string) int {
		return managementDoctor(doctorJSON, version)
	})
	doctor.Flags().BoolVar(&doctorJSON, "json", false, "print diagnostic checks as JSON")
	versionCmd := managementCommand("version", "Usage: ghok version\n", code, func(_ *cobra.Command, _ []string) int {
		return managementVersion(version, commit)
	})

	hookCommands := managementCommand("hooks", "Usage: ghok hooks list [--json]\n       ghok hooks test [--phase pre|post] [--json] -- <gh args>\n", code, managementGroup)
	var listJSON bool
	list := managementCommand("list", "Usage: ghok hooks list [--json]\n", code, func(_ *cobra.Command, _ []string) int {
		return managementHooksList(listJSON)
	})
	list.Flags().BoolVar(&listJSON, "json", false, "print JSON")
	var testJSON bool
	phase := config.PhasePre
	test := managementCommand("test", "Usage: ghok hooks test [--phase pre|post] [--json] -- <gh args>\n", code, func(_ *cobra.Command, args []string) int {
		return managementHooksTest(args, phase, testJSON)
	})
	test.Args = managementInvocationArgs
	test.Flags().BoolVar(&testJSON, "json", false, "print JSON")
	test.Flags().StringVar(&phase, "phase", config.PhasePre, "hook phase: pre or post")
	hookCommands.AddCommand(list, test)

	for _, name := range []string{"normalize", "explain"} {
		var options managementInspectOptions
		cmd := managementCommand(name, "Usage: ghok "+name+" [options] -- <gh args>\n", code, func(cmd *cobra.Command, args []string) int {
			return managementInspect(cmd.Name(), args, options)
		})
		cmd.Args = managementInvocationArgs
		cmd.Flags().BoolVar(&options.resolveTargets, "resolve", false, "resolve targets from local git metadata")
		cmd.Flags().BoolVar(&options.allowAPI, "allow-api", false, "allow read-only API resolution (requires --resolve)")
		if name == "normalize" {
			cmd.Flags().Bool("json", true, "print the normalization result as JSON (the only output format)")
		} else {
			cmd.Flags().BoolVar(&options.showSecrets, "show-secrets", false, "show secrets in explain output; never affects hook payload redaction")
		}
		root.AddCommand(cmd)
	}

	configCommands := managementCommand("config", "Usage: ghok config <validate|path|schema>\n", code, managementGroup)
	var file string
	validate := managementCommand("validate", "Usage: ghok config validate\n", code, func(_ *cobra.Command, _ []string) int {
		return managementConfig("validate", file)
	})
	validate.Flags().StringVar(&file, "file", "", "global configuration file to validate")
	path := managementCommand("path", "Usage: ghok config path\n", code, func(_ *cobra.Command, _ []string) int {
		return managementConfig("path", "")
	})
	var project bool
	schema := managementCommand("schema", "Usage: ghok config schema [--project]\n", code, func(_ *cobra.Command, _ []string) int {
		_, _ = os.Stdout.Write(config.SchemaJSON(project))
		return 0
	})
	schema.Flags().BoolVar(&project, "project", false, "print the schema for a project .ghok.yaml instead of config.yaml")
	configCommands.AddCommand(validate, path, schema)
	root.AddCommand(shim, doctor, versionCmd, hookCommands, configCommands)
	root.SetHelpCommand(&cobra.Command{
		Use:  "help [command]",
		Args: cobra.ArbitraryArgs,
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

func managementCommand(use, usage string, code *int, run func(*cobra.Command, []string) int) *cobra.Command {
	cmd := &cobra.Command{
		Use:  use,
		Args: managementCommandArgs,
		Run: func(cmd *cobra.Command, args []string) {
			*code = run(cmd, args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd.Parent() == nil && len(cmd.Flags().Args()) != 0 {
			*code = managementUsageError("help does not accept arguments")
			return
		}
		_, _ = fmt.Fprint(os.Stdout, usage)
		// Keep the established help layout while taking all flag metadata from
		// pflag, which is the only parser used by management commands.
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name == "help" {
				return
			}
			_, _ = fmt.Fprintf(os.Stdout, "  -%s", flag.Name)
			if flag.Value.Type() == "string" {
				_, _ = fmt.Fprint(os.Stdout, " string")
			}
			_, _ = fmt.Fprint(os.Stdout, "\n    \t"+flag.Usage)
			if flag.Value.Type() == "string" && flag.DefValue != "" {
				_, _ = fmt.Fprintf(os.Stdout, " (default %q)", flag.DefValue)
			} else if flag.Value.Type() == "bool" && flag.DefValue != "false" {
				_, _ = fmt.Fprintf(os.Stdout, " (default %s)", flag.DefValue)
			}
			_, _ = fmt.Fprintln(os.Stdout)
		})
	})
	return cmd
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

func managementGroup(cmd *cobra.Command, _ []string) int {
	_ = cmd.Help()
	return 2
}

func managementFlagError(_ *cobra.Command, err error) error {
	// Preserve existing diagnostics using pflag's structured errors, not a
	// second parser or string matching against library-generated messages.
	var unknown *pflag.NotExistError
	var required *pflag.ValueRequiredError
	var invalid *pflag.InvalidValueError
	switch {
	case errors.As(err, &unknown):
		return fmt.Errorf("flag provided but not defined: -%s", unknown.GetSpecifiedName())
	case errors.As(err, &required):
		return fmt.Errorf("flag needs an argument: -%s", required.GetSpecifiedName())
	case errors.As(err, &invalid) && invalid.GetFlag().Value.Type() == "bool":
		return fmt.Errorf("invalid boolean value %q for -%s: parse error", invalid.GetValue(), invalid.GetFlag().Name)
	default:
		return err
	}
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
		_, _ = fmt.Fprintln(os.Stdout, r.String("Note: "+note))
	}
	if err != nil {
		return managementFailure(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, r.String("Configuration valid: "+path+" (missing files use defaults)"))
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
	cfgCheck := install.Check{Name: "config", Status: install.StatusOK, Detail: config.DefaultPath(env) + " (missing files use defaults)"}
	if loadErr != nil {
		cfgCheck.Status, cfgCheck.Detail = install.StatusFail, loadErr.Error()
		cfgCheck.Fix = "Run ghok config validate and fix the reported configuration errors."
	}
	checks = append(checks, cfgCheck)
	for _, note := range notes {
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
