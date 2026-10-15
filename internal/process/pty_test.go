//go:build unix

package process

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/mkusaka/ghok/internal/model"
)

// pollablePTY makes Close interrupt blocked test reads instead of waiting for
// a blocking read syscall. Keep one owner for the duplicated descriptor.
func pollablePTY(t *testing.T, f *os.File) *os.File {
	t.Helper()
	fd, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_DUPFD_CLOEXEC, 3)
	if errno != 0 {
		_ = f.Close()
		t.Fatal(errno)
	}
	if err := syscall.SetNonblock(int(fd), true); err != nil {
		_ = syscall.Close(int(fd))
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	file := os.NewFile(fd, "test-pty")
	if err := file.SetReadDeadline(time.Time{}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return file
}

func startPTY(t *testing.T, cfg wrapperConfig) (*commandWait, *os.File, *bufio.Reader) {
	t.Helper()
	cmd := wrapperCommand(t, cfg)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	f = pollablePTY(t, f)
	w := beginCommandWait(cmd, func() {
		// Darwin exiting processes can wait for unread tty output: release
		// the master before reaping, including when an assertion failed.
		_ = f.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	t.Cleanup(func() { cleanupCommand(t, w) })
	return w, f, bufio.NewReader(f)
}

func waitPTYCommand(t *testing.T, w *commandWait, f *os.File, r *bufio.Reader) syscall.WaitStatus {
	t.Helper()
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, r); close(drained) }()
	status := waitCommand(t, w)
	_ = f.Close()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("PTY drain did not finish after master close")
	}
	return status
}

func TestPTYCleanupWithUnreadOutput(t *testing.T) {
	w, master, _ := startPTY(t, childConfig(t, "output", 64<<20))
	if err := master.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var first [1]byte
	if _, err := io.ReadFull(master, first[:]); err != nil {
		t.Fatal(err)
	}
	// Leave the remaining tty output unread; cleanup must still close/reap.
	cleanupCommand(t, w)
}

func TestSpoolTTYIsNotConsumed(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	master = pollablePTY(t, master)
	slave = pollablePTY(t, slave)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	spool, err := SpoolStdin(slave, 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if spool.Data().Status != model.StdinTTY || spool.File() != slave || spool.RequiresSupervision() {
		t.Fatalf("terminal spool status %s pump %v", spool.Data().Status, spool.RequiresSupervision())
	}
	// Input written after inspection remains available on the original fd.
	if _, err := master.Write([]byte("untouched\n")); err != nil {
		t.Fatal(err)
	}
	if got := lineWithin(t, bufio.NewReader(slave)); got != "untouched" {
		t.Fatal(got)
	}
}

func TestPTYTerminalAndResize(t *testing.T) {
	for _, mode := range []string{"exec", "run"} {
		cfg := childConfig(t, "metadata", 0)
		cfg.Mode = mode
		cmd, master, r := startPTY(t, cfg)
		s := lineWithin(t, r)
		var got metadata
		if err := json.Unmarshal([]byte(s), &got); err != nil {
			t.Fatal(err)
		}
		if got.TTY != [3]bool{true, true, true} {
			t.Fatalf("TTY fds %#v", got.TTY)
		}
		if status := waitPTYCommand(t, cmd, master, r); !status.Exited() || status.ExitStatus() != 0 {
			t.Fatal(status)
		}
	}
	cfg := childConfig(t, "metadata", 0)
	cfg.Capture = true
	cmd, master, r := startPTY(t, cfg)
	var got metadata
	if err := json.Unmarshal([]byte(lineWithin(t, r)), &got); err != nil {
		t.Fatal(err)
	}
	if got.TTY != [3]bool{true, false, false} {
		t.Fatalf("capture TTY %#v", got.TTY)
	}
	_ = waitPTYCommand(t, cmd, master, r)
	cmd, f, r := startPTY(t, childConfig(t, "resize", 0))
	if lineWithin(t, r) != "READY" {
		t.Fatal("not ready")
	}
	if err := pty.Setsize(f, &pty.Winsize{Rows: 41, Cols: 103}); err != nil {
		t.Fatal(err)
	}
	if got := lineWithin(t, r); got != "SIZE 41 103" {
		t.Fatal(got)
	}
	if status := waitPTYCommand(t, cmd, f, r); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatal(status)
	}
}

func TestPTYInterruptExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		sig syscall.Signal
		key byte
	}{{syscall.SIGINT, 3}, {syscall.SIGQUIT, 28}} {
		t.Run(tc.sig.String(), func(t *testing.T) {
			cmd, f, r := startPTY(t, childConfig(t, "signal", int(tc.sig)))
			if !strings.HasPrefix(lineWithin(t, r), "READY ") {
				t.Fatal("not ready")
			}
			if _, err := f.Write([]byte{tc.key}); err != nil {
				t.Fatal(err)
			}
			line := lineWithin(t, r)
			if !strings.HasSuffix(line, "COUNT 1") {
				t.Fatalf("terminal delivered %q", line)
			}
			status := waitPTYCommand(t, cmd, f, r)
			if !status.Signaled() || status.Signal() != tc.sig {
				t.Fatalf("status %v", status)
			}
		})
	}
}

// Observe stop events directly, without cmd.Wait consuming WUNTRACED statuses.
func stoppedWithin(t *testing.T, pid int, want syscall.Signal) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var status syscall.WaitStatus
		got, err := syscall.Wait4(pid, &status, syscall.WUNTRACED|syscall.WNOHANG, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			if stoppedSignal(status) != want {
				t.Fatalf("stop status %v want %v", status, want)
			}
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("wrapper failed to mirror stop")
		}
	}
}

func TestChildStopContinue(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU} {
		for _, ignoreContinue := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ignored-cont=%v", sig, ignoreContinue), func(t *testing.T) {
				cfg := childConfig(t, "block", 0)
				if ignoreContinue {
					// SIGCONT still resumes a stopped process when its notification
					// is ignored. This deterministically removes asynchronous CONT
					// delivery: the stop/continue handoff itself must resume the child.
					cfg.Mode = "run-ignore-cont"
				}
				cmd := wrapperCommand(t, cfg)
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				inR, inW, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				outR, outW, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				cmd.Stdin = inR
				cmd.Stdout = outW
				if err = cmd.Start(); err != nil {
					t.Fatal(err)
				}
				_ = inR.Close()
				_ = outW.Close()
				var waiter *commandWait
				t.Cleanup(func() {
					_ = inW.Close()
					_ = outR.Close()
					if waiter == nil {
						waiter = beginCommandWait(cmd, nil)
					}
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					cleanupCommand(t, waiter)
				})
				line := lineWithin(t, bufio.NewReader(outR))
				var pid int
				if _, err = fmt.Sscanf(line, "READY %d", &pid); err != nil {
					t.Fatal(err)
				}
				if err = syscall.Kill(pid, sig); err != nil {
					t.Fatal(err)
				}
				stoppedWithin(t, cmd.Process.Pid, sig)
				waiter = beginCommandWait(cmd, nil)
				if err = cmd.Process.Signal(syscall.SIGCONT); err != nil {
					t.Fatal(err)
				}
				_ = inW.Close()
				if status := waitCommand(t, waiter); !status.Exited() || status.ExitStatus() != 0 {
					t.Fatal(status)
				}
			})
		}
	}
}

func TestChildSIGSTOPContinueDoesNotStopWrapper(t *testing.T) {
	for _, ignoreContinue := range []bool{false, true} {
		t.Run(fmt.Sprintf("ignored-cont=%v", ignoreContinue), func(t *testing.T) {
			cfg := childConfig(t, "block", 0)
			cfg.ResultFile = filepath.Join(t.TempDir(), "result")
			if ignoreContinue {
				cfg.Mode = "run-ignore-cont"
			}
			w, r, input := startPiped(t, cfg)
			var pid int
			if _, err := fmt.Sscanf(lineWithin(t, r), "READY %d", &pid); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
				t.Fatal(err)
			}
			// Keep the child stopped while the supervisor processes its wait event.
			// No CONT is ever sent to the wrapper, including during cleanup.
			if err := w.waitWithin(200 * time.Millisecond); err != os.ErrDeadlineExceeded {
				t.Fatalf("wrapper exited before child CONT: %v", err)
			}
			if !childStillStopped(pid) {
				t.Fatal("child did not remain stopped")
			}
			if childStillStopped(w.Process.Pid) {
				t.Fatal("wrapper mirrored child-only SIGSTOP")
			}
			if err := syscall.Kill(pid, syscall.SIGCONT); err != nil {
				t.Fatal(err)
			}
			_ = input.Close()
			if status := waitCommand(t, w); !status.Exited() || status.ExitStatus() != 0 {
				t.Fatalf("wrapper failed to complete after child-only CONT: %v", status)
			}
			b, err := os.ReadFile(cfg.ResultFile)
			if err != nil {
				t.Fatal(err)
			}
			var result struct{ Code, Signal int }
			if err := json.Unmarshal(b, &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != 0 || result.Signal != 0 {
				t.Fatalf("Run result: %+v", result)
			}
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Fatalf("child not reaped: %v", err)
			}
		})
	}
}

func TestHandledTerminalStopDoesNotStopWrapper(t *testing.T) {
	w, r, input := startPiped(t, childConfig(t, "handled-stop", 0))
	if !strings.HasPrefix(lineWithin(t, r), "READY ") {
		t.Fatal("child not ready")
	}
	if err := syscall.Kill(-w.Process.Pid, syscall.SIGTSTP); err != nil {
		t.Fatal(err)
	}
	if got := lineWithin(t, r); got != "HANDLED" {
		t.Fatalf("child stop handler: %q", got)
	}
	_ = input.Close()
	if status := waitCommand(t, w); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("wrapper stopped despite a running child: %v", status)
	}
}

func TestShellForegroundBackgroundJobs(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("interactive bash unavailable for PTY fg/bg scenario")
	}
	cfg := childConfig(t, "job", 0)
	wrapper := wrapperCommand(t, cfg)
	cmd := testCommand(t, bash, "--noprofile", "--norc", "--noediting", "-i")
	cmd.Env = append(wrapper.Env, "PS1=", "PS2=", "TERM=dumb")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	f = pollablePTY(t, f)
	jobGroup := 0
	w := beginCommandWait(cmd, func() {
		_ = f.Close()
		if jobGroup != 0 {
			_ = syscall.Kill(-jobGroup, syscall.SIGKILL)
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	t.Cleanup(func() { cleanupCommand(t, w) })
	r := bufio.NewReader(f)
	// Mark prompts with explicit commands, so no timeout/sleep-based prompt polling.
	_, _ = f.WriteString("stty -echo; printf 'SHELL_READY\\n'\n")
	for lineWithin(t, r) != "SHELL_READY" {
	}
	_, _ = fmt.Fprintf(f, "'%s'\n", wrapper.Path)
	for {
		line := lineWithin(t, r)
		if i := strings.Index(line, "READY "); i >= 0 {
			var pid int
			if _, err := fmt.Sscanf(line[i:], "READY %d", &pid); err != nil {
				t.Fatal(err)
			}
			jobGroup, err = syscall.Getpgid(pid)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	_, _ = f.Write([]byte{26})
	for !strings.Contains(lineWithin(t, r), "Stopped") {
	}
	_, _ = fmt.Fprintln(f, "bg; printf 'BG_RESUMED\\n'")
	// The child reads its terminal while backgrounded and gets SIGTTIN.
	for lineWithin(t, r) != "BG_RESUMED" {
	}
	_, _ = fmt.Fprintln(f, "wait %1; jobs -l; printf 'BG_CHECK_DONE\\n'")
	stopped := false
	for {
		line := lineWithin(t, r)
		if strings.Contains(line, "Stopped") {
			stopped = true
		}
		if line == "BG_CHECK_DONE" {
			break
		}
	}
	if !stopped {
		t.Fatal("shell did not observe stopped background stdin reader")
	}
	_, _ = f.WriteString("fg; code=$?; printf 'FG_DONE:%s\\n' \"$code\"; jobs -l; printf 'FG_JOBS_DONE\\n'\n")
	// Collect all foreground markers in one phase: neither a CONT acknowledgment
	// nor a nonzero shell result may be discarded while awaiting another marker.
	completion := ""
	sent, childDone := false, false
	var jobs []string
	for {
		line := lineWithin(t, r)
		if line == "FOREGROUND" && !sent {
			sent = true
			if _, err := f.WriteString("DONE\n"); err != nil {
				t.Fatal(err)
			}
		}
		if line == "CHILD_DONE" {
			childDone = true
		}
		if strings.HasPrefix(line, "FG_DONE:") {
			completion = line
		}
		if line == "FG_JOBS_DONE" {
			break
		}
		jobs = append(jobs, line)
	}
	if completion != "FG_DONE:0" || !childDone {
		t.Fatalf("foreground completion %q; child acknowledged DONE=%v; jobs/output:\n%s", completion, childDone, strings.Join(jobs, "\n"))
	}
	// The shell's interactive exit-confirmation policy is not under test.
	_, _ = f.WriteString("exec /usr/bin/true\n")
	if status := waitPTYCommand(t, w, f, r); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatal(status)
	}
}
