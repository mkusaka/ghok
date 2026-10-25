//go:build unix

// Package delegates selects read-only execution replacements and implements the
// not-handled protocol. Policy, hooks and transforms remain the caller's job.
package delegates

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/process"
)

// NotHandled is reserved by the delegate protocol, not a normal gh exit code.
const NotHandled = 125

// Selection is safe for diagnostics: never include delegate env values.
type Selection struct {
	ID      string          `json:"id"`
	Command []string        `json:"command"`
	Timeout config.Duration `json:"timeout"`
	OnError string          `json:"onError"`
	Action  string          `json:"action"`
	Repo    string          `json:"repository,omitempty"`
}

// Select returns only the first definite match, and only when the whole
// invocation is read-only. A per-operation match cannot hide another write.
func Select(c *config.Config, s policy.Subject) *Selection {
	if s.Env["GHOK_NO_DELEGATE"] == "1" || !safe(s.Result) {
		return nil
	}
	for _, d := range c.Delegates {
		if yes, indexes := policy.Matches(d.Match, d.Unless, s); yes && len(indexes) > 0 {
			op := s.Result.Operations[indexes[0]]
			command := append([]string(nil), d.Command...)
			command[0] = c.DelegateCommandPath(d)
			return &Selection{ID: d.ID, Command: command, Timeout: d.Timeout, OnError: d.OnError, Action: op.Action, Repo: op.Target.Repository}
		}
	}
	return nil
}

func safe(r model.Result) bool {
	if !r.Classification.MutationFree || len(r.Errors) > 0 || len(r.Operations) == 0 {
		return false
	}
	switch r.Classification.Level {
	case model.LevelL1:
		if r.Classification.Confidence != model.ConfidenceExact {
			return false
		}
	case model.LevelL2:
		if r.Source.Kind != "rest" || r.Source.Method != "GET" {
			return false
		}
	default:
		return false
	}
	for _, op := range r.Operations {
		if op.Execution != model.ExecutionSkipped && op.Execution != model.ExecutionNotSelected && (op.Effect != model.EffectRead || op.UnknownReason != model.UnknownNone) {
			return false
		}
	}
	return true
}

// CommandPath searches the effective delegate PATH, not the wrapper's PATH.
// A path with a slash is already resolved relative to its config directory.
func CommandPath(name string, env []string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		return filepath.Abs(name)
	}
	search := ""
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			search = value
		}
	}
	permission := false
	for _, dir := range filepath.SplitList(search) {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			permission = true
			continue
		}
		return filepath.Abs(candidate)
	}
	err := syscall.ENOENT
	if permission {
		err = syscall.EACCES
	}
	return "", &os.PathError{Op: "exec", Path: name, Err: err}
}

func Environment(base []string, overrides map[string]string, selected *Selection, real string) []string {
	values := make(map[string]string, len(base)+len(overrides)+5)
	for _, entry := range base {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	for name, value := range overrides {
		values[name] = value
	}
	// These cannot be disabled by delegate-specific env overrides.
	values["GHOK_NO_DELEGATE"] = "1"
	values["GHOK_DELEGATE_ID"] = selected.ID
	values["GHOK_ACTION"] = selected.Action
	values["GHOK_REPO"] = selected.Repo
	values["GHOK_REAL_GH"] = real
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	return env
}

// Run supervises the delegate and, when safe, real gh once (never the next
// delegate). A warning is returned to the caller before real gh starts.
func Run(selected *Selection, overrides map[string]string, real process.Options, warn func(string)) process.Result {
	started := time.Now()
	fail := func(message string) process.Result {
		warn(message + "; not running gh")
		return process.Result{ExitCode: 78, Duration: time.Since(started)}
	}
	source := os.Stdin
	if real.Stdin != nil {
		// Analysis with an incomplete live spool cannot safely be read by two
		// pumps. Such requests retain the ordinary real-gh execution path.
		if real.Stdin.RequiresSupervision() {
			return process.Run(context.Background(), real)
		}
		source = real.Stdin.File()
	}
	replay, err := process.NewInputReplay(source)
	if err != nil {
		return fail("cannot preserve delegate stdin: " + err.Error())
	}
	defer replay.Close()
	opts := real
	opts.Env = Environment(real.Env, overrides, selected, real.Path)
	opts.Path, err = CommandPath(selected.Command[0], opts.Env)
	opts.Args = append(append([]string(nil), selected.Command...), real.Args[1:]...)
	opts.Stdin = replay.Attempt()
	// Streaming tee is required to know whether timeout/not-handled retry is
	// safe. Retain bytes only if post hooks requested capture.
	opts.Capture = true
	if !real.Capture {
		opts.CaptureLimit = 0
	}
	opts.DrainTimeout = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), selected.Timeout.D())
	var result process.Result
	if err != nil {
		result = process.Result{ExitCode: -1, StartErr: err}
	} else {
		result = process.Run(ctx, opts)
	}
	timedOut := ctx.Err() != nil
	cancel()
	// Signals always win, including a delegate that catches one and declines.
	if result.Interrupted != 0 || result.Signal != 0 && !timedOut {
		return result
	}
	out, errout := result.Stdout, result.Stderr
	output := func(c *process.Captured) bool { return c != nil && (c.Size > 0 || c.Error != nil) }
	reason := ""
	switch {
	case result.StartErr != nil:
		reason = "command could not start: " + result.StartErr.Error()
	case timedOut:
		if output(out) || output(errout) {
			return fail("timed out after output; fallback is unsafe")
		}
		reason = "timed out before output"
	case result.ExitCode == NotHandled:
		if output(out) {
			return fail("exit 125 after stdout; delegate violated the not-handled contract")
		}
		reason = "not handled (exit 125)"
	default:
		return result
	}
	if result.WaitErr != nil {
		return fail("cannot safely retry after wait error: " + result.WaitErr.Error())
	}
	if selected.OnError == config.OnErrorDeny && (timedOut || result.StartErr != nil) {
		return fail(reason)
	}
	real.Stdin, err = replay.Fallback()
	if err != nil {
		return fail(err.Error())
	}
	warn(fmt.Sprintf("%s; falling back to real gh", reason))
	result = process.Run(context.Background(), real)
	result.Duration = time.Since(started)
	return result
}
