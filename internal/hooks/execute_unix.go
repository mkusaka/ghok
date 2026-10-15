//go:build unix

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

// boundedOutput drains excess bytes so a noisy child cannot deadlock on a pipe.
type boundedOutput struct {
	data      []byte
	limit     int64
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - int64(len(b.data))
	if int64(n) > left {
		b.truncated = true
		p = p[:int(left)]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (r *Runner) environment(h config.Hook, in protocol.Input) []string {
	base := r.Env
	if base == nil {
		base = os.Environ()
	}
	values := make(map[string]string, len(base)+len(h.Env)+5)
	keys := make([]string, 0, len(base)+len(h.Env)+5)
	set := func(k, v string) {
		if _, ok := values[k]; !ok {
			keys = append(keys, k)
		}
		values[k] = v
	}
	var nestedToken string
	for _, entry := range base {
		if k, v, ok := strings.Cut(entry, "="); ok {
			set(k, v)
			if k == "GHOK_NESTED_TOKEN" {
				nestedToken = v
			}
		}
	}
	for k, v := range h.Env {
		set(k, v)
	}
	set("GHOK_EVENT", in.Event)
	set("GHOK_EVENT_ID", in.EventID)
	set("GHOK_HOOK_ID", h.ID)
	set("GHOK_NESTED", "1")
	set("GHOK_NESTED_TOKEN", nestedToken)
	if r.RealGh != "" {
		set("GHOK_REAL_GH", r.RealGh)
	}
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+values[k])
	}
	return env
}

// exec.LookPath reads the wrapper's PATH, not Cmd.Env. Search the hook's PATH
// explicitly without changing global environment (parallel hooks are allowed).
func (r *Runner) commandPath(name string, env []string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if !filepath.IsAbs(name) {
			name = filepath.Join(r.BaseDir, name)
		}
		return filepath.Abs(name)
	}
	path := ""
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == "PATH" {
			path = v
		}
	}
	var permission bool
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() || info.Mode()&0111 == 0 {
			permission = true
			continue
		}
		return filepath.Abs(candidate)
	}
	if permission {
		return "", &os.PathError{Op: "exec", Path: name, Err: syscall.EACCES}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func missingExecutable(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.ENOEXEC)
}

func (r *Runner) execute(parent context.Context, phase string, h config.Hook, in protocol.Input) (result protocol.HookResult) {
	start := r.now()
	result = protocol.HookResult{HookID: h.ID, Phase: phase, ExitCode: -1}
	defer func() {
		result.DurationMs = r.now().Sub(start).Milliseconds()
		result.Reason = r.redact(result.Reason)
		result.Message = r.redact(result.Message)
		result.Error = r.redact(result.Error)
		result.Stderr = r.redact(result.Stderr)
		result.Effective = effective(phase, h, result)
	}()
	fail := func(err error) {
		result.Outcome = protocol.OutcomeError
		result.Error = err.Error()
		if h.Optional && missingExecutable(err) {
			result.Outcome = protocol.OutcomeSkipped
			result.Reason = "optional command unavailable"
		}
	}
	if len(h.Command) == 0 || h.Command[0] == "" {
		fail(errors.New("empty hook command"))
		return
	}
	data, err := json.Marshal(in)
	if err != nil {
		fail(fmt.Errorf("encode hook input: %w", err))
		return
	}
	env := r.environment(h, in)
	path, err := r.commandPath(h.Command[0], env)
	if err != nil {
		fail(err)
		return
	}
	timeout := h.Timeout.D()
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if ctx.Err() != nil {
		result.Outcome = protocol.OutcomeCanceled
		result.Error = ctx.Err().Error()
		return
	}
	stdoutLimit, stderrLimit := h.MaxOutputBytes, h.MaxOutputBytes
	if stdoutLimit <= 0 {
		stdoutLimit = DefaultMaxOutputBytes
		stderrLimit = DefaultMaxStderrBytes
	}
	stdout := &boundedOutput{limit: stdoutLimit}
	stderr := &boundedOutput{limit: stderrLimit}
	cmd := exec.Command(path, h.Command[1:]...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// os/exec closes copy pipes if descendants retain descriptors after exit.
	cmd.WaitDelay = TerminationGrace
	if err = cmd.Start(); err != nil {
		fail(err)
		return
	}
	result.Started = true
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	canceled := false
	select {
	case err = <-waited:
		if errors.Is(err, exec.ErrWaitDelay) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	case <-ctx.Done():
		canceled = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		// Kill the group even if the leader exits early: grandchildren can ignore
		// TERM and keep running after Wait returns. Always reap the direct child.
		timer := time.NewTimer(TerminationGrace)
		<-timer.C
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-waited
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	result.Stderr = string(stderr.data)
	if stderr.truncated {
		result.Stderr += "\n[stderr truncated]"
	}
	if canceled {
		result.Outcome = protocol.OutcomeCanceled
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Outcome = protocol.OutcomeTimeout
		}
		result.Error = ctx.Err().Error()
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if len(bytes.TrimSpace(stdout.data)) == 0 && !stdout.truncated {
		result.Outcome = protocol.OutcomeOK
		result.Decision = protocol.DecisionAbstain
		return
	}
	var response protocol.Response
	decoder := json.NewDecoder(bytes.NewReader(stdout.data))
	err = decoder.Decode(&response)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); !errors.Is(trailing, io.EOF) {
			err = errors.New("trailing data after hook response")
		}
	}
	if err == nil && response.SchemaVersion != 0 && response.SchemaVersion != 1 {
		err = fmt.Errorf("unsupported schemaVersion %d", response.SchemaVersion)
	}
	if err == nil && response.Decision != protocol.DecisionAllow && response.Decision != protocol.DecisionDeny && response.Decision != protocol.DecisionAbstain {
		err = fmt.Errorf("invalid hook decision %q", response.Decision)
	}
	// A truncated prefix cannot establish that the unseen suffix was valid JSON.
	if stdout.truncated {
		err = errors.New("hook stdout exceeded output limit")
	}
	if err != nil {
		result.Outcome = protocol.OutcomeInvalidResponse
		result.Error = err.Error()
		return
	}
	result.Outcome = protocol.OutcomeOK
	result.Decision = response.Decision
	result.Reason = response.Reason
	result.Message = response.Message
	return
}
