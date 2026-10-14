//go:build unix

package resolve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"
)

type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	available := b.limit - b.buffer.Len()
	if len(p) > available {
		b.buffer.Write(p[:available])
		b.overflow = true
		b.cancel()
		return available, fmt.Errorf("output limit exceeded")
	}
	return b.buffer.Write(p)
}

func runAPI(ctx context.Context, realGh, cwd string, env map[string]string, args []string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, realGh, args...)
	cmd.Dir = cwd
	keys := make([]string, 0, len(env))
	for key := range env {
		if key != "GHOK_NESTED" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	cmd.Env = make([]string, 0, len(keys)+1)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	cmd.Env = append(cmd.Env, "GHOK_NESTED=1")
	// nil Stdin is /dev/null. These noninteractive helper processes need their
	// own group so cancellation kills shell/credential-helper descendants too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// A helper that exits but leaves pipe-holding descendants must not hang us.
	cmd.WaitDelay = 100 * time.Millisecond
	stdout := &boundedOutput{limit: maxBytes, cancel: cancel}
	stderr := &boundedOutput{limit: 64 << 10, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if stdout.overflow {
		return nil, fmt.Errorf("resolver API stdout exceeds 1 MiB")
	}
	if stderr.overflow {
		return nil, fmt.Errorf("resolver API stderr exceeds 64 KiB")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("resolver API: %w", ctx.Err())
	}
	if err != nil {
		// Do not expose stderr: gh/helper failures can contain tokens or URLs.
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("resolver API exited with status %d", exit.ExitCode())
		}
		return nil, fmt.Errorf("resolver API failed: %w", err)
	}
	return stdout.buffer.Bytes(), nil
}
