//go:build unix

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Options supplies the real executable, its full argv (including argv[0]), and
// exact environment. A nil Env means an empty environment, not inheritance.
type Options struct {
	Path         string
	Args         []string
	Env          []string
	Stdin        *Spool
	Capture      bool
	CaptureLimit int64
}

// Result records the child's wait status. ExitCode is -1 for signal termination.
type Result struct {
	ExitCode int
	Signal   syscall.Signal
	CoreDump bool
	Duration time.Duration
	StartErr error
	// WaitErr reports an unexpected failure to reap the child.
	WaitErr        error
	Stdout, Stderr *Captured
}

// Exec replaces the wrapper; it returns only on failure. A live stdin replay
// pump or output capture needs Run instead. Failed exec restores the original fd 0.
func Exec(opts Options) (err error) {
	if opts.Stdin != nil {
		defer opts.Stdin.Close()
	}
	if len(opts.Args) == 0 || opts.Capture || (opts.Stdin != nil && opts.Stdin.RequiresSupervision()) {
		return fmt.Errorf("exec requires argv and file-backed, uncaptured stdio: %w", syscall.EINVAL)
	}
	if opts.Stdin != nil && opts.Stdin.File() != os.Stdin {
		saved, e := syscall.Dup(0)
		if e != nil {
			return e
		}
		syscall.CloseOnExec(saved)
		defer syscall.Close(saved)
		if e = dupStandardInput(int(opts.Stdin.File().Fd())); e != nil {
			return e
		}
		defer func() {
			if e := dupStandardInput(saved); e != nil {
				err = errors.Join(err, e)
			}
		}()
	}
	return syscall.Exec(opts.Path, opts.Args, opts.Env)
}

// StartErrorExitCode follows shell spawn-error conventions.
func StartErrorExitCode(err error) int {
	if errors.Is(err, syscall.ENOENT) {
		return 127
	}
	return 126
}

// Run supervises one child in the caller's process group. It must be used by
// the wrapper's main execution path, not concurrent with another Run or a
// separate wait/signal owner. Context cancellation allows one second for TERM.
func Run(ctx context.Context, opts Options) (result Result) {
	start := time.Now()
	result.ExitCode = -1
	defer func() { result.Duration = time.Since(start) }()
	if opts.Stdin != nil {
		defer opts.Stdin.Close()
	}
	if len(opts.Args) == 0 {
		result.StartErr = syscall.EINVAL
		return
	}
	if err := ctx.Err(); err != nil {
		result.StartErr = err
		return
	}

	// Check Ignored BEFORE Notify: inherited nohup/background-job dispositions
	// must not turn into caught signals in the child.
	sigs := make(chan os.Signal, 32)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGCONT} {
		if !signal.Ignored(sig) {
			signal.Notify(sigs, sig)
		}
	}
	defer signal.Stop(sigs)
	stdin, stdout, stderr := os.Stdin, os.Stdout, os.Stderr
	if opts.Stdin != nil {
		stdin = opts.Stdin.File()
	}
	var cap *capture
	if opts.Capture {
		var err error
		cap, err = startCapture(opts.CaptureLimit)
		if err != nil {
			result.StartErr = err
			return
		}
		defer cap.close()
		stdout, stderr = cap.stdout, cap.stderr
	}
	pid, err := syscall.ForkExec(opts.Path, opts.Args, &syscall.ProcAttr{
		Env:   opts.Env,
		Files: []uintptr{stdin.Fd(), stdout.Fd(), stderr.Fd()},
	})
	if cap != nil {
		cap.closeWrites()
	}
	if err != nil {
		result.StartErr = err
		return
	}
	var finishPump func()
	if opts.Stdin != nil && opts.Stdin.RequiresSupervision() {
		finishPump, err = opts.Stdin.startPump()
		if err != nil {
			// Spawn succeeded: kill/reap, and report the input delivery failure
			// separately rather than falsely claiming that gh was never started.
			result.WaitErr = err
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	if finishPump != nil {
		defer func() {
			if finishPump != nil {
				finishPump()
			}
		}()
	}
	type waitEvent struct {
		status syscall.WaitStatus
		err    error
	}
	waits := make(chan waitEvent)
	go func() {
		for {
			var status syscall.WaitStatus
			_, err := syscall.Wait4(pid, &status, syscall.WUNTRACED, nil)
			if err == syscall.EINTR {
				continue
			}
			waits <- waitEvent{status, err}
			if err != nil || status.Exited() || status.Signaled() {
				return
			}
		}
	}()
	cancel := ctx.Done()
	var grace *time.Timer
	var kill <-chan time.Time
	defer func() {
		if grace != nil {
			grace.Stop()
		}
	}()
	for {
		select {
		case ev := <-waits:
			if ev.err != nil {
				result.WaitErr = errors.Join(result.WaitErr, ev.err)
				return
			}
			if sig := stoppedSignal(ev.status); sig != 0 {
				// Stop this pid, never the whole group. Orphaned groups may
				// ignore terminal stop signals; SIGSTOP itself cannot be ignored.
				// A group stop may have stopped us before Wait4's event was
				// handled; after shell fg/bg that old event must not stop us again.
				if childStillStopped(pid) {
					_ = syscall.Kill(os.Getpid(), sig)
				}
				continue
			}
			if ev.status.Signaled() {
				result.Signal = ev.status.Signal()
				result.CoreDump = ev.status.CoreDump()
			} else {
				result.ExitCode = ev.status.ExitStatus()
			}
			if finishPump != nil {
				finishPump()
				finishPump = nil
			}
			if cap != nil {
				stopAbort := context.AfterFunc(ctx, cap.close)
				result.Stdout, result.Stderr = cap.finish()
				stopAbort()
			}
			return
		case s := <-sigs:
			sig := s.(syscall.Signal)
			if (sig == syscall.SIGINT || sig == syscall.SIGQUIT) && foregroundTTY() {
				continue
			}
			_ = syscall.Kill(pid, sig)
		case <-cancel:
			cancel = nil
			_ = syscall.Kill(pid, syscall.SIGCONT)
			_ = syscall.Kill(pid, syscall.SIGTERM)
			grace = time.NewTimer(time.Second)
			kill = grace.C
			if cap != nil {
				cap.close()
			}
		case <-kill:
			kill = nil
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// Wait4 is called without WCONTINUED. Decode stop statuses directly: Go's BSD
// WaitStatus.Stopped reports a real SIGSTOP stop as Continued (syscall_bsd.go).
func stoppedSignal(status syscall.WaitStatus) syscall.Signal {
	if status&0xff == 0x7f {
		return syscall.Signal(status>>8) & 0xff
	}
	return 0
}

func ioctl(fd uintptr, request uintptr, value unsafe.Pointer) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(value))
	if err != 0 {
		return err
	}
	return nil
}

func isTerminal(f *os.File) bool {
	var size [4]uint16
	return f != nil && ioctl(f.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&size)) == nil
}

func foregroundTTY() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		var pg int32
		if ioctl(f.Fd(), syscall.TIOCGPGRP, unsafe.Pointer(&pg)) == nil {
			return int(pg) == syscall.Getpgrp()
		}
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	var pg int32
	return ioctl(f.Fd(), syscall.TIOCGPGRP, unsafe.Pointer(&pg)) == nil && int(pg) == syscall.Getpgrp()
}

// Exit reproduces normal versus signaled termination, not just 128+signal.
// Wrapper core dumps are disabled; CoreDump still describes the child.
func (r Result) Exit() {
	if r.StartErr != nil {
		os.Exit(StartErrorExitCode(r.StartErr))
	}
	if r.WaitErr != nil && r.Signal == 0 && r.ExitCode < 0 {
		os.Exit(126)
	}
	if r.Signal == 0 {
		os.Exit(r.ExitCode)
	}
	_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{})
	// Unblock and raise on the same thread. Reset alone does not remove Go's
	// handlers for synchronous signals, nor inherited SIG_IGN dispositions.
	runtime.LockOSThread()
	signal.Reset(r.Signal)
	if defaultSignal(r.Signal) == nil {
		raiseSignal(r.Signal)
	}
	os.Exit(128 + int(r.Signal))
}
