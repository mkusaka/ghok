//go:build unix

package e2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/mkusaka/ghok/internal/config"
)

type terminalEvent struct {
	Event      string `json:"event"`
	StdinTTY   bool   `json:"stdinTTY"`
	StdoutTTY  bool   `json:"stdoutTTY"`
	StderrTTY  bool   `json:"stderrTTY"`
	Rows       uint16 `json:"rows"`
	Cols       uint16 `json:"cols"`
	Interrupts int    `json:"interrupts"`
	Input      string `json:"input"`
}

func TestPTYResizeAndCtrlCExactlyOnce(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		t.Run(fmt.Sprintf("supervised=%v", supervised), func(t *testing.T) {
			s := newSandbox(t)
			if supervised {
				s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("post", "post")} })
			}
			s.env["FAKEGH_MODE"] = "pty"
			cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, "version")
			defer cancel()
			cmd.Stdin = nil
			cmd.SysProcAttr = nil
			terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
			if err != nil {
				if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
					t.Skipf("PTY unsupported: %v", err)
				}
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				_ = terminal.Close()
				if !waited {
					_ = cmd.Wait()
				}
			})
			lines := make(chan string, 32)
			go func() {
				defer close(lines)
				reader := bufio.NewScanner(terminal)
				for reader.Scan() {
					lines <- reader.Text()
				}
			}()
			next := func(want string) terminalEvent {
				t.Helper()
				deadline := time.NewTimer(15 * time.Second)
				defer deadline.Stop()
				for {
					select {
					case line, ok := <-lines:
						if !ok {
							t.Fatalf("PTY closed before %s", want)
						}
						start := strings.IndexByte(line, '{')
						if start < 0 {
							continue
						}
						var event terminalEvent
						if err := json.Unmarshal([]byte(line[start:]), &event); err != nil {
							t.Fatalf("PTY event: %v (%q)", err, line)
						}
						if event.Event == want {
							return event
						}
					case <-deadline.C:
						t.Fatalf("PTY timed out waiting for %s", want)
					}
				}
			}
			initial := next("ready")
			if !initial.StdinTTY || !initial.StdoutTTY || !initial.StderrTTY || initial.Rows != 24 || initial.Cols != 80 {
				t.Fatalf("terminal metadata=%+v", initial)
			}
			typed := []byte("interactive stdin\n")
			if _, err := terminal.Write(typed); err != nil {
				t.Fatal(err)
			}
			if received := next("input"); received.Input != string(typed) {
				t.Fatalf("interactive stdin=%q, want=%q", received.Input, typed)
			}
			if err := pty.Setsize(terminal, &pty.Winsize{Rows: 43, Cols: 117}); err != nil {
				t.Fatal(err)
			}
			resized := next("resize")
			if resized.Rows != 43 || resized.Cols != 117 {
				t.Fatalf("terminal resize=%+v", resized)
			}
			if _, err := terminal.Write([]byte{3}); err != nil {
				t.Fatal(err)
			}
			interrupt := next("interrupt")
			if interrupt.Interrupts != 1 {
				t.Fatalf("terminal delivered %d interrupts, want exactly 1", interrupt.Interrupts)
			}
			err = cmd.Wait()
			waited = true
			outcome{err: err}.code(t, 0)
			s.onlyRecord([]string{"version"}, typed)
			if supervised {
				input := s.hookInput("post")
				if !input.Invocation.IsTTY || !input.Invocation.StdinTTY || input.Result == nil || input.Result.ExitCode != 0 {
					t.Fatalf("PTY hook protocol=%+v", input)
				}
			}
		})
	}
}
