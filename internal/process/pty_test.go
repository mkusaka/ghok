//go:build unix

package process

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/mkusaka/ghok/internal/model"
)

func startPTY(t *testing.T, cfg wrapperConfig) (*exec.Cmd, *os.File, *bufio.Reader) {
	t.Helper()
	cmd := wrapperCommand(t, cfg)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait(); f.Close() })
	return cmd, f, bufio.NewReader(f)
}

func TestSpoolTTYIsNotConsumed(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	spool, err := SpoolStdin(slave, 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
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
		cmd, _, r := startPTY(t, cfg)
		s := lineWithin(t, r)
		var got metadata
		if err := json.Unmarshal([]byte(s), &got); err != nil {
			t.Fatal(err)
		}
		if got.TTY != [3]bool{true, true, true} {
			t.Fatalf("TTY fds %#v", got.TTY)
		}
		if status := waitCommand(t, cmd); !status.Exited() || status.ExitStatus() != 0 {
			t.Fatal(status)
		}
	}
	cfg := childConfig(t, "metadata", 0)
	cfg.Capture = true
	cmd, _, r := startPTY(t, cfg)
	var got metadata
	if err := json.Unmarshal([]byte(lineWithin(t, r)), &got); err != nil {
		t.Fatal(err)
	}
	if got.TTY != [3]bool{true, false, false} {
		t.Fatalf("capture TTY %#v", got.TTY)
	}
	_ = waitCommand(t, cmd)
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
	if status := waitCommand(t, cmd); !status.Exited() || status.ExitStatus() != 0 {
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
			status := waitCommand(t, cmd)
			if !status.Signaled() || status.Signal() != tc.sig {
				t.Fatalf("status %v", status)
			}
		})
	}
}

// Observe stop events directly, without cmd.Wait consuming WUNTRACED statuses.
func stoppedWithin(t *testing.T, pid int, want syscall.Signal) {
	t.Helper()
	done := make(chan struct {
		status syscall.WaitStatus
		err    error
	}, 1)
	go func() {
		var status syscall.WaitStatus
		_, err := syscall.Wait4(pid, &status, syscall.WUNTRACED, nil)
		done <- struct {
			status syscall.WaitStatus
			err    error
		}{status, err}
	}()
	select {
	case v := <-done:
		if v.err != nil || stoppedSignal(v.status) != want {
			t.Fatalf("stop status %v err %v want %v", v.status, v.err, want)
		}
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGCONT)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		<-done
		t.Fatal("wrapper failed to mirror stop")
	}
}

func TestChildStopContinue(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU} {
		t.Run(sig.String(), func(t *testing.T) {
			cfg := childConfig(t, "block", 0)
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
			inR.Close()
			outW.Close()
			t.Cleanup(func() { inW.Close(); outR.Close(); _ = cmd.Process.Kill() })
			line := lineWithin(t, bufio.NewReader(outR))
			var pid int
			if _, err = fmt.Sscanf(line, "READY %d", &pid); err != nil {
				t.Fatal(err)
			}
			if err = syscall.Kill(pid, sig); err != nil {
				t.Fatal(err)
			}
			stoppedWithin(t, cmd.Process.Pid, sig)
			if err = cmd.Process.Signal(syscall.SIGCONT); err != nil {
				t.Fatal(err)
			}
			inW.Close()
			if status := waitCommand(t, cmd); !status.Exited() || status.ExitStatus() != 0 {
				t.Fatal(status)
			}
		})
	}
}

func TestShellForegroundBackgroundJobs(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("interactive bash unavailable for PTY fg/bg scenario")
	}
	cfg := childConfig(t, "job", 0)
	wrapper := wrapperCommand(t, cfg)
	cmd := exec.Command(bash, "--noprofile", "--norc", "-i")
	cmd.Env = append(wrapper.Env, "PS1=GHOK_PROMPT> ", "PS2=GHOK_MORE> ", "TERM=dumb")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	jobGroup := 0
	t.Cleanup(func() {
		if jobGroup != 0 {
			_ = syscall.Kill(-jobGroup, syscall.SIGKILL)
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		f.Close()
	})
	r := bufio.NewReader(f)
	// Mark prompts with explicit commands, so no timeout/sleep-based prompt polling.
	_, _ = fmt.Fprintf(f, "printf 'SHELL_READY\\n'\n")
	for {
		if strings.Contains(lineWithin(t, r), "SHELL_READY") {
			break
		}
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
	for {
		if strings.Contains(lineWithin(t, r), "Stopped") {
			break
		}
	}
	_, _ = fmt.Fprintln(f, "bg; printf 'BG_RESUMED\\n'")
	// The child reads its terminal while backgrounded and gets SIGTTIN.
	for {
		if lineWithin(t, r) == "BG_RESUMED" {
			break
		}
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
	_, _ = f.WriteString("fg; printf 'FG_DONE:%s\\n' \"$?\"\n")
	// The child's CONT acknowledgment and bash's job description can arrive in
	// either order. Wait only for the child's foreground acknowledgment.
	for {
		if lineWithin(t, r) == "FOREGROUND" {
			break
		}
	}
	// A complete line is a deterministic child completion handshake; terminal
	// EOF markers can be consumed by the shell around tcsetpgrp/fg transitions.
	_, _ = f.WriteString("DONE\n")
	for {
		line := lineWithin(t, r)
		if strings.Contains(line, "FG_DONE:") {
			if !strings.Contains(line, "FG_DONE:0") {
				t.Fatalf("foreground completion %q", line)
			}
			break
		}
	}
	// The shell's interactive exit-confirmation policy is not under test.
	_, _ = f.WriteString("exec /usr/bin/true\n")
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, r); close(drained) }()
	if status := waitCommand(t, cmd); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatal(status)
	}
	<-drained
}
