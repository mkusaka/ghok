//go:build unix

// Package cli wires the management commands and transparent gh wrapper.
package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/native"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/process"
	"github.com/mkusaka/ghok/internal/protocol"
)

func environment() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func realGh(c *config.Config, env map[string]string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	override := env["GHOK_REAL_GH"]
	if override == "" && c != nil {
		override = c.Runtime.RealGh
	}
	real, _, err := install.FindRealGh(install.DiscoverOptions{Self: self, PathEnv: env["PATH"], Override: override})
	return real, err
}

func invocation(c *config.Config, args []string, env map[string]string, cwd string, stdin model.StdinData) model.Invocation {
	inv := model.Invocation{Argv: args, Env: env, Cwd: cwd, Stdin: stdin}
	inv.ReadFile = func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		limit := c.Runtime.FileMaxBytes
		if limit < math.MaxInt64 {
			limit++
		}
		data, err := io.ReadAll(io.LimitReader(f, limit))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > c.Runtime.FileMaxBytes {
			return nil, fmt.Errorf("input too large: file exceeds %d bytes", c.Runtime.FileMaxBytes)
		}
		return data, nil
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && !native.IsBuiltinCommand(args[0]) {
		// Failed alias loading leaves a non-built-in command explicitly unknown.
		inv.Aliases, _ = config.LoadGhAliases(env)
	}
	return inv
}

func expandedArgv(inv model.Invocation) []string {
	args, _, reason := native.ExpandAlias(inv.Argv, inv.Aliases)
	if reason != model.UnknownNone {
		return inv.Argv
	}
	return args
}

func hookInput(args []string, cwd, real string, result model.Result, ctx *model.ResolvedContext, evaluation policy.Evaluation, eventID string) protocol.Input {
	return protocol.Input{
		SchemaVersion: model.SchemaVersion, EventID: eventID,
		Invocation:     protocol.InvocationInfo{Argv: args, Alias: result.Source.Alias, Expanded: result.Source.Argv, Cwd: cwd, RealGh: real, IsTTY: install.IsTTY(os.Stdout), StdinTTY: install.IsTTY(os.Stdin), TokenKind: policy.TokenKind(environment())},
		Classification: result.Classification, Operations: result.Operations, Errors: result.Errors, Context: ctx,
		Policy: &protocol.PolicyInfo{Mode: evaluation.Mode, Decision: evaluation.Decision, Reasons: evaluation.Reasons, Rules: evaluation.Rules},
	}
}

func execInfo(result process.Result) *protocol.ExecResult {
	info := &protocol.ExecResult{ExitCode: result.ExitCode, Signal: signalName(result.Signal), CoreDump: result.CoreDump, DurationMs: result.Duration.Milliseconds()}
	if result.StartErr != nil {
		info.StartError = result.StartErr.Error()
	}
	capture := func(out *process.Captured) *protocol.Output {
		if out == nil {
			return nil
		}
		return &protocol.Output{Base64: base64.StdEncoding.EncodeToString(out.Data), Size: out.Size, Truncated: out.Truncated}
	}
	info.Stdout, info.Stderr = capture(result.Stdout), capture(result.Stderr)
	return info
}

func signalName(sig syscall.Signal) string {
	if sig == 0 {
		return ""
	}
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGILL:
		return "SIGILL"
	case syscall.SIGTRAP:
		return "SIGTRAP"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGBUS:
		return "SIGBUS"
	case syscall.SIGFPE:
		return "SIGFPE"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGSTOP:
		return "SIGSTOP"
	case syscall.SIGTSTP:
		return "SIGTSTP"
	case syscall.SIGCONT:
		return "SIGCONT"
	case syscall.SIGTTIN:
		return "SIGTTIN"
	case syscall.SIGTTOU:
		return "SIGTTOU"
	default:
		return fmt.Sprintf("SIG%d", sig)
	}
}
