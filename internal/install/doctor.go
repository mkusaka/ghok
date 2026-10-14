package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusInfo Status = "info"
)

type Check struct {
	Name   string
	Status Status
	Detail string
	Fix    string
}
type DoctorOptions struct {
	Self     string
	Version  string
	PathEnv  string
	Override string
	// BinDir is the fallback when state has no recorded shims.
	BinDir   string
	StateDir string
	// Env is passed to diagnostics and inspected for GH_PATH / GHOK_DEPTH.
	// nil inherits the process environment. Non-nil (even empty) is exact.
	Env []string
	// Stdin defaults to os.Stdin and is inspected, never read.
	Stdin *os.File
}

// Checks returns independent diagnostics; config/hook checks can be appended.
func Checks(o DoctorOptions) []Check {
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	binary := Check{Name: "ghok", Status: StatusOK, Detail: o.Self + " (version " + o.Version + ")"}
	if info, err := os.Stat(o.Self); err != nil {
		binary.Status, binary.Detail = StatusFail, fmt.Sprintf("cannot stat ghok %q: %v", o.Self, err)
	} else if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		binary.Status, binary.Detail = StatusFail, "ghok is not a regular executable file: "+o.Self
	}
	checks := []Check{binary}
	real, candidates, err := FindRealGh(DiscoverOptions{PathEnv: o.PathEnv, Self: o.Self, Override: o.Override})
	if err != nil {
		checks = append(checks, Check{Name: "real-gh", Status: StatusFail, Detail: err.Error(), Fix: "Install GitHub CLI or set runtime.realGh / GHOK_REAL_GH to its absolute executable path."})
	} else {
		checks = append(checks, Check{Name: "real-gh", Status: StatusOK, Detail: real})
	}
	state, shimErr := loadState(o.StateDir)
	shims := make([]string, 0, len(state))
	for shim := range state {
		shims = append(shims, shim)
	}
	sort.Strings(shims)
	if shimErr == nil && len(shims) == 0 {
		var shim string
		shim, shimErr = shimPath(o.BinDir)
		if shimErr == nil {
			shims = append(shims, shim)
		}
	}
	if shimErr != nil {
		checks = append(checks, Check{Name: "path-precedence", Status: StatusFail, Detail: shimErr.Error()}, Check{Name: "shim-ownership", Status: StatusFail, Detail: shimErr.Error()})
	} else {
		for _, shim := range shims {
			_, detail, fix := pathPrecedence(shim, o.PathEnv)
			status := StatusOK
			if fix != "" {
				status = StatusWarn
			}
			checks = append(checks, Check{Name: "path-precedence", Status: status, Detail: detail, Fix: fix})
			owned, err := ownedShim(shim, o.Self, state)
			ownership := Check{Name: "shim-ownership", Status: StatusOK, Detail: shim + " is an owned symlink"}
			if err != nil {
				ownership.Status, ownership.Detail = StatusFail, shim+": "+err.Error()
			} else if !owned {
				ownership.Status, ownership.Detail = StatusWarn, shim+" is absent or not managed by ghok"
				ownership.Fix = "Run ghok shim install with --bin-dir set to " + strconv.Quote(filepath.Dir(shim)) + "; resolve any foreign path collision first."
			}
			checks = append(checks, ownership)
		}
	}
	recursion := Check{Name: "recursion-risk", Status: StatusOK, Detail: "Discovery excludes self and ghok-named executables; nesting is bounded by GHOK_DEPTH."}
	var risks []string
	if _, _, err := NextDepth(env); err != nil {
		risks = append(risks, err.Error())
	}
	if o.Override != "" {
		for _, c := range candidates {
			if c.Skipped == "ghok shim" {
				risks = append(risks, "real gh override points at a ghok shim")
			}
		}
	}
	for _, entry := range env {
		value, ok := strings.CutPrefix(entry, "GH_PATH=")
		if !ok || value == "" {
			continue
		}
		owned, ownErr := IsOwnedShim(value, o.Self, o.StateDir)
		self, _ := os.Stat(o.Self)
		candidate := inspectCandidate(value, self)
		if owned || candidate.Skipped == "ghok shim" {
			risks = append(risks, "GH_PATH points at a ghok shim; real gh propagates this path to extensions and credential helpers")
		} else if ownErr != nil {
			risks = append(risks, "cannot verify GH_PATH ownership: "+ownErr.Error())
		}
	}
	if real != "" {
		owned, ownErr := IsOwnedShim(real, o.Self, o.StateDir)
		if owned {
			risks = append(risks, "discovery selected a recorded shim for another renamed ghok installation")
		} else if ownErr != nil {
			risks = append(risks, "cannot verify discovered executable ownership: "+ownErr.Error())
		}
	}
	if len(risks) > 0 {
		recursion.Status, recursion.Detail = StatusFail, strings.Join(risks, "; ")
		recursion.Fix = "Point the real gh override at GitHub CLI, unset shim-valued GH_PATH, and inspect nested gh invocations."
	}
	checks = append(checks, recursion)
	version := Check{Name: "gh-version", Status: StatusInfo, Detail: "Not checked: no real gh was discovered."}
	// Never execute a discovered known shim, even a renamed one caught by state.
	owned, _ := IsOwnedShim(real, o.Self, o.StateDir)
	if real != "" && !owned {
		out, err := runDiagnostic(real, env, "--version")
		if err != nil {
			version.Status, version.Detail = StatusWarn, "gh --version failed: "+err.Error()
		} else {
			version.Status, version.Detail = ghVersionStatus(out)
		}
		if version.Status == StatusWarn {
			version.Fix = "Use GitHub CLI 2.102.0 or newer; 2.102.0 is the verified normalization baseline."
		}
	}
	checks = append(checks, version)
	stdin := o.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	detail := fmt.Sprintf("%s/%s; stdin tty=%t; ", runtime.GOOS, runtime.GOARCH, stdinTTY(stdin))
	tty, ttyErr := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if ttyErr == nil {
		_ = tty.Close()
		detail += "/dev/tty available; "
	} else {
		detail += "/dev/tty unavailable (no controlling terminal or access denied); "
	}
	detail += "Unix job control uses the foreground process group; supervised gh must remain in that group."
	if runtime.GOOS == "linux" {
		info, err := os.Stat("/proc")
		detail += fmt.Sprintf(" /proc available=%t.", err == nil && info.IsDir())
	}
	checks = append(checks, Check{Name: "platform-process", Status: StatusInfo, Detail: detail})
	return checks
}

var ghVersionRE = regexp.MustCompile(`(?m)^gh version ([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][^ ]*)?(?: |$)`)

func ghVersionStatus(out string) (Status, string) {
	m := ghVersionRE.FindStringSubmatch(out)
	if m == nil {
		return StatusWarn, "Unrecognized gh version output: " + strings.TrimSpace(out)
	}
	var version [3]int
	for i := range version {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return StatusWarn, "Unrecognized gh version number"
		}
		version[i] = n
	}
	if version[0] < 2 || version[0] == 2 && version[1] < 102 {
		return StatusWarn, "gh " + strings.Join(m[1:4], ".") + " predates the verified 2.102.0 baseline"
	}
	return StatusInfo, strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]) + "; verified baseline is 2.102.0"
}

// Bounded diagnostics cannot grow stdout indefinitely or wait for inherited
// pipes after timeout (for example, a script that spawns a background child).
type diagnosticOutput struct {
	data      []byte
	truncated bool
}

func (w *diagnosticOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 64*1024 - len(w.data)
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	w.data = append(w.data, p...)
	return n, nil
}

// diagnosticTimeout bounds `gh --version` and probes; generous because a cold
// start on a loaded machine can take seconds. Tests shorten it.
var diagnosticTimeout = 10 * time.Second

func runDiagnostic(path string, env []string, args ...string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), diagnosticTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, abs, args...)
	cmd.Env = env
	cmd.WaitDelay = 100 * time.Millisecond
	var out diagnosticOutput
	cmd.Stdout = &out
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("diagnostic timed out after %s: %w", diagnosticTimeout, ctx.Err())
	}
	if err != nil {
		return "", err
	}
	if out.truncated {
		return "", fmt.Errorf("diagnostic stdout exceeds 64 KiB")
	}
	return string(out.data), nil
}
