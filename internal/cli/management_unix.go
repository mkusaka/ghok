//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
	"github.com/mkusaka/ghok/internal/redact"
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
  help [command]              Show help

Resolution is local unless --allow-api is explicitly supplied with --resolve.
Explain never executes hooks or the original gh command and exits 0 even on deny.
Exit status: 0 success, 1 failure/denial, 2 usage error.
`

// Management runs the management CLI. It never executes the user's gh command.
func Management(args []string, version, commit string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(os.Stdout, managementUsage)
		return 0
	}
	if args[0] == "help" {
		if len(args) == 1 {
			_, _ = fmt.Fprint(os.Stdout, managementUsage)
			return 0
		}
		return Management(append(append([]string(nil), args[1:]...), "--help"), version, commit)
	}
	switch args[0] {
	case "-h", "--help":
		if len(args) != 1 {
			return managementUsageError("help does not accept arguments")
		}
		_, _ = fmt.Fprint(os.Stdout, managementUsage)
		return 0
	case "shim":
		return managementShim(args[1:])
	case "doctor":
		return managementDoctor(args[1:], version)
	case "version":
		fs := managementFlags("version", "ghok version")
		if code := managementParse(fs, args[1:], false); code >= 0 {
			return code
		}
		if version == "" {
			version = "dev"
		}
		_, _ = fmt.Fprintf(os.Stdout, "ghok %s\n", version)
		if commit != "" {
			_, _ = fmt.Fprintf(os.Stdout, "commit %s\n", commit)
		}
		_, _ = fmt.Fprintf(os.Stdout, "%s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return 0
	case "hooks":
		return managementHooks(args[1:])
	case "normalize", "explain":
		return managementInspect(args[0], args[1:])
	case "config":
		return managementConfig(args[1:])
	default:
		return managementUsageError("unknown command " + args[0])
	}
}

func managementFlags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stdout, "Usage: %s\n", usage)
		fs.SetOutput(os.Stdout)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
	return fs
}

// -1 means successfully parsed; all other values are CLI exit statuses.
func managementParse(fs *flag.FlagSet, args []string, invocationArgs bool) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return managementUsageError(err.Error())
	}
	if !invocationArgs && fs.NArg() != 0 {
		return managementUsageError("unexpected argument " + fs.Arg(0))
	}
	if invocationArgs {
		// Require the documented separator so gh flags cannot become ghok flags.
		separator := len(args) - fs.NArg() - 1
		if separator < 0 || args[separator] != "--" || fs.NArg() == 0 {
			return managementUsageError("provide gh arguments after --")
		}
	}
	return -1
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

func managementShim(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(os.Stdout, "Usage: ghok shim <install|uninstall> [--bin-dir DIR]")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	command := args[0]
	if command != "install" && command != "uninstall" {
		return managementUsageError("unknown shim command " + command)
	}
	args = args[1:]
	env := environment()
	fs := managementFlags("shim "+command, "ghok shim "+command+" [--bin-dir DIR]")
	bin := fs.String("bin-dir", filepath.Join(env["HOME"], ".local", "bin"), "directory containing the gh shim")
	if code := managementParse(fs, args, false); code >= 0 {
		return code
	}
	if *bin == "" {
		return managementUsageError("--bin-dir must not be empty")
	}
	self, err := os.Executable()
	if err != nil {
		return managementFailure(err)
	}
	state := filepath.Join(config.Dir(env), "state")
	r := redact.New(env)
	if command == "uninstall" {
		removed, err := install.Disable(install.DisableOptions{BinDir: *bin, Self: self, StateDir: state})
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
	result, err := install.Enable(install.EnableOptions{BinDir: *bin, Self: self, StateDir: state, PathEnv: env["PATH"]})
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

func managementConfig(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(os.Stdout, "Usage: ghok config <validate|path>")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if args[0] != "validate" && args[0] != "path" {
		return managementUsageError("unknown config command " + args[0])
	}
	fs := managementFlags("config "+args[0], "ghok config "+args[0])
	var file string
	if args[0] == "validate" {
		fs.StringVar(&file, "file", "", "global configuration file to validate")
	}
	if code := managementParse(fs, args[1:], false); code >= 0 {
		return code
	}
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
	if args[0] == "path" {
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

func managementDoctor(args []string, version string) int {
	fs := managementFlags("doctor", "ghok doctor [--json]")
	asJSON := fs.Bool("json", false, "print diagnostic checks as JSON")
	if code := managementParse(fs, args, false); code >= 0 {
		return code
	}
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
	if *asJSON {
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
