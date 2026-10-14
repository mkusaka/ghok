//go:build unix

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

const commandTimeout = 15 * time.Second
const commandReapTimeout = 5 * time.Second

// Bound both the process lifetime and exec's pipe-copy shutdown for Run/Output.
func testCommand(t testing.TB, path string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = commandReapTimeout
	return cmd
}

// A fixture keeps this waiter for both normal completion and cleanup. Only this
// goroutine owns cmd.Wait; a timeout never starts another waiter.
type commandWait struct {
	*exec.Cmd
	done       chan struct{}
	err        error
	beforeReap func()
	closeOnce  sync.Once
}

func beginCommandWait(cmd *exec.Cmd, beforeReap func()) *commandWait {
	w := &commandWait{Cmd: cmd, done: make(chan struct{}), beforeReap: beforeReap}
	go func() {
		w.err = cmd.Wait()
		close(w.done)
	}()
	return w
}

func (w *commandWait) waitWithin(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return w.err
	case <-timer.C:
		return os.ErrDeadlineExceeded
	}
}

func (w *commandWait) killAndReap(timeout time.Duration) error {
	// Darwin can keep a killed process exiting while unread tty output remains.
	// Closing the master (and any blocked copy source) must precede reaping.
	w.closeOnce.Do(func() {
		if w.beforeReap != nil {
			w.beforeReap()
		}
	})
	select {
	case <-w.done:
		return w.err
	default:
	}
	killErr := w.Process.Kill()
	err := w.waitWithin(timeout)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("wait did not reap pid %d after SIGKILL (%v): %w", w.Process.Pid, killErr, err)
	}
	return err
}

func waitCommand(t *testing.T, w *commandWait) syscall.WaitStatus {
	t.Helper()
	err := w.waitWithin(commandTimeout)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		reapErr := w.killAndReap(commandReapTimeout)
		t.Fatalf("command pid %d did not exit: %v; kill/reap: %v", w.Process.Pid, err, reapErr)
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
	return w.ProcessState.Sys().(syscall.WaitStatus)
}

func cleanupCommand(t testing.TB, w *commandWait) {
	t.Helper()
	if err := w.killAndReap(commandReapTimeout); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Errorf("command cleanup: %v", err)
		}
	}
}

func TestCommandWaitBlockedPipeAndRepeatedCleanup(t *testing.T) {
	cfg := childConfig(t, "output", 2<<20)
	cmd := testCommand(t, cfg.Path)
	cmd.Args, cmd.Env = cfg.Args, cfg.Env
	r, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = out.Close() })
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
	w := beginCommandWait(cmd, func() { _ = r.Close() })
	t.Cleanup(func() { cleanupCommand(t, w) })
	if err := r.SetReadDeadline(time.Now().Add(commandReapTimeout)); err != nil {
		t.Fatal(err)
	}
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		t.Fatal(err)
	}
	if err := w.waitWithin(20 * time.Millisecond); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("unread output pipe wait = %v; want timeout", err)
	}
	cleanupCommand(t, w)
	cleanupCommand(t, w)
	if err := w.waitWithin(commandReapTimeout); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("repeated wait = %v; want the same completed exit error", err)
	}
}
