//go:build unix

package process

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const helperKey = "GHOK_PROCESS_HELPER"

type wrapperConfig struct {
	Mode       string
	Path       string
	Args       []string
	Env        []string
	Capture    bool
	Limit      int64
	Spool      bool
	SpoolLimit int64
	Timeout    time.Duration
	Cancel     time.Duration
	ResultFile string
}

type metadata struct {
	Args, Env []string
	Cwd       string
	Pgrp      int
	TTY       [3]bool
	Ignored   map[string]bool
	FDs       []int
	FDInfo    map[int]string
}

func TestMain(m *testing.M) {
	switch os.Getenv(helperKey) {
	case "wrapper":
		wrapperMain()
		os.Exit(99)
	case "child":
		childMain()
		os.Exit(0)
	case "ignore":
		sig, _ := strconv.Atoi(os.Getenv("GHOK_IGNORE_SIGNAL"))
		signal.Ignore(syscall.Signal(sig))
		env := os.Environ()
		for i, value := range env {
			if strings.HasPrefix(value, helperKey+"=") {
				env[i] = helperKey + "=wrapper"
			}
		}
		if err := syscall.Exec(os.Args[0], os.Args, env); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

func wrapperMain() {
	var cfg wrapperConfig
	if err := json.Unmarshal([]byte(os.Getenv("GHOK_WRAPPER_CONFIG")), &cfg); err != nil {
		panic(err)
	}
	opts := Options{Path: cfg.Path, Args: cfg.Args, Env: cfg.Env, Capture: cfg.Capture, CaptureLimit: cfg.Limit}
	if cfg.Spool {
		var err error
		opts.Stdin, err = SpoolStdin(os.Stdin, cfg.SpoolLimit, cfg.Timeout)
		if err != nil {
			if opts.Stdin != nil {
				opts.Stdin.Close()
			}
			os.Exit(125)
		}
	}
	if cfg.Mode == "exec" {
		err := Exec(opts)
		os.Exit(StartErrorExitCode(err))
	}
	if cfg.Mode == "run-ignore-cont" {
		signal.Ignore(syscall.SIGCONT)
	}
	ctx := context.Background()
	if cfg.Cancel > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Cancel)
		defer cancel()
	}
	r := Run(ctx, opts)
	if cfg.ResultFile != "" {
		b, _ := json.Marshal(struct {
			Code     int
			Signal   int
			Out, Err *Captured
		}{r.ExitCode, int(r.Signal), r.Stdout, r.Stderr})
		_ = os.WriteFile(cfg.ResultFile, b, 0600)
	}
	r.Exit()
}

func childMain() {
	watchdog := time.AfterFunc(commandTimeout, func() { os.Exit(124) })
	defer watchdog.Stop()
	mode := os.Getenv("GHOK_CHILD_MODE")
	n, _ := strconv.Atoi(os.Getenv("GHOK_CHILD_N"))
	switch mode {
	case "metadata":
		cwd, _ := os.Getwd()
		m := metadata{Args: os.Args, Env: os.Environ(), Cwd: cwd, Pgrp: syscall.Getpgrp(), Ignored: map[string]bool{}}
		imagePath, _ := os.Executable()
		for i, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
			m.TTY[i] = isTerminal(f)
		}
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGPIPE, syscall.SIGTERM} {
			m.Ignored[strconv.Itoa(int(sig))] = signal.Ignored(sig)
		}
		for fd := 3; fd < 128; fd++ {
			_, _, err := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
			if err == 0 {
				if m.FDInfo == nil {
					m.FDInfo = make(map[int]string)
				}
				target, _ := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
				flags, _, _ := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
				m.FDInfo[fd] = fmt.Sprintf("%s cloexec=%d", target, flags)
				// Executable image fds are owned by the loader, not process.
				// Rosetta retains another image fd for each exec boundary.
				if target != imagePath || imagePath == "" {
					m.FDs = append(m.FDs, fd)
				}
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(m)
	case "cat":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "binary":
		_, _ = os.Stdout.Write([]byte{0, 1, 255, 0, 10})
		_, _ = os.Stderr.Write([]byte{255, 0, 2, 10})
	case "output":
		chunk := bytes.Repeat([]byte{0, 1, 2, 255}, 8192)
		for n > 0 {
			b := chunk
			if n < len(b) {
				b = b[:n]
			}
			if _, err := os.Stdout.Write(b); err != nil {
				os.Exit(44)
			}
			n -= len(b)
		}
	case "exit":
		os.Exit(n)
	case "kill":
		Result{Signal: syscall.Signal(n)}.Exit()
	case "stream":
		fmt.Println("first")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Println("second")
	case "signal":
		sig := syscall.Signal(n)
		ch := make(chan os.Signal, 16)
		signal.Notify(ch, sig)
		fmt.Printf("READY %d\n", os.Getpid())
		<-ch
		count := 1
		delay := time.NewTimer(150 * time.Millisecond)
	loop:
		for {
			select {
			case <-ch:
				count++
			case <-delay.C:
				break loop
			}
		}
		fmt.Printf("COUNT %d\n", count)
		Result{Signal: sig}.Exit()
	case "block":
		fmt.Printf("READY %d\n", os.Getpid())
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "handled-stop":
		stopped := make(chan os.Signal, 1)
		signal.Notify(stopped, syscall.SIGTSTP)
		defer signal.Stop(stopped)
		fmt.Printf("READY %d\n", os.Getpid())
		<-stopped
		fmt.Println("HANDLED")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "job":
		continued := make(chan os.Signal, 8)
		signal.Notify(continued, syscall.SIGCONT)
		go func() {
			for range continued {
				if foregroundTTY() {
					fmt.Println("FOREGROUND")
				}
			}
		}()
		fmt.Printf("READY %d\n", os.Getpid())
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || line != "DONE\n" {
			fmt.Printf("JOB_READ_ERROR line=%q err=%v foreground=%v\n", line, err, foregroundTTY())
			os.Exit(41)
		}
		fmt.Println("CHILD_DONE")
	case "ignoreterm":
		signal.Ignore(syscall.SIGTERM)
		fmt.Printf("READY %d\n", os.Getpid())
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "resize":
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGWINCH)
		fmt.Println("READY")
		<-ch
		size, err := pty.GetsizeFull(os.Stdin)
		if err != nil {
			panic(err)
		}
		fmt.Printf("SIZE %d %d\n", size.Rows, size.Cols)
	case "grandchild":
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGTERM)
		path, err := os.Executable()
		if err != nil {
			panic(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path)
		cmd.WaitDelay = commandReapTimeout
		cmd.Env = []string{helperKey + "=child", "GHOK_CHILD_MODE=block"}
		r, w, _ := os.Pipe()
		cmd.Stdin = r
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		r.Close()
		waiter := beginCommandWait(cmd, func() { _ = w.Close() })
		fmt.Printf("READY %d\n", cmd.Process.Pid)
		select {
		case <-ch:
		case <-ctx.Done():
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = w.Close()
		if err := waiter.waitWithin(commandReapTimeout); err != nil {
			if err := waiter.killAndReap(commandReapTimeout); err != nil {
				if _, ok := err.(*exec.ExitError); !ok {
					panic(err)
				}
			}
		}
		fmt.Println("REAPED")
		Result{Signal: syscall.SIGTERM}.Exit()
	}
}

func childConfig(t testing.TB, mode string, n int) wrapperConfig {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return wrapperConfig{Mode: "run", Path: path, Args: []string{"gh", "space arg", "", "日本語", "--dash"}, Env: []string{helperKey + "=child", "GHOK_CHILD_MODE=" + mode, "GHOK_CHILD_N=" + strconv.Itoa(n), "EXACT=one two"}, Limit: 7, SpoolLimit: 1024, Timeout: 5 * time.Second}
}

func wrapperCommand(t testing.TB, cfg wrapperConfig) *exec.Cmd {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd := testCommand(t, path)
	cmd.Env = []string{helperKey + "=wrapper", "GHOK_WRAPPER_CONFIG=" + string(b), "HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "TMPDIR=" + t.TempDir()}
	return cmd
}

func lineWithin(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	done := make(chan struct {
		s   string
		err error
	}, 1)
	go func() {
		s, err := r.ReadString('\n')
		done <- struct {
			s   string
			err error
		}{s, err}
	}()
	select {
	case v := <-done:
		if v.err != nil {
			t.Fatalf("read line: %v (%q)", v.err, v.s)
		}
		return strings.TrimSpace(v.s)
	case <-time.After(10 * time.Second):
		t.Fatal("line timeout")
		return ""
	}
}

func startPiped(t *testing.T, cfg wrapperConfig) (*commandWait, *bufio.Reader, *os.File) {
	t.Helper()
	cmd := wrapperCommand(t, cfg)
	// Keep pid-directed INT/QUIT tests outside any inherited foreground tty group.
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
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inR.Close()
	outW.Close()
	waiter := beginCommandWait(cmd, func() { _ = inW.Close(); _ = outR.Close() })
	t.Cleanup(func() {
		_ = inW.Close()
		_ = outR.Close()
		select {
		case <-waiter.done:
		default:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cleanupCommand(t, waiter)
	})
	return waiter, bufio.NewReader(outR), inW
}

func TestArgvEnvironmentCwdAndFDs(t *testing.T) {
	for _, mode := range []string{"exec", "run"} {
		t.Run(mode, func(t *testing.T) {
			cfg := childConfig(t, "metadata", 0)
			cfg.Mode = mode
			direct := testCommand(t, cfg.Path)
			direct.Args = cfg.Args
			direct.Env = cfg.Env
			direct.Dir = t.TempDir()
			b, err := direct.Output()
			if err != nil {
				t.Fatal(err)
			}
			var baseline metadata
			if err = json.Unmarshal(b, &baseline); err != nil {
				t.Fatal(err)
			}
			cmd := wrapperCommand(t, cfg)
			cmd.Dir = direct.Dir
			b, err = cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var got metadata
			if err = json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if strings.Join(got.Args, "\x00") != strings.Join(cfg.Args, "\x00") || strings.Join(got.Env, "\x00") != strings.Join(cfg.Env, "\x00") || got.Cwd != baseline.Cwd {
				t.Fatalf("metadata %#v want argv/env/cwd %#v", got, baseline)
			}
			if fmt.Sprint(got.FDs) != fmt.Sprint(baseline.FDs) {
				t.Fatalf("stray inherited descriptors %v direct %v; targets got %v direct %v", got.FDs, baseline.FDs, got.FDInfo, baseline.FDInfo)
			}
			if got.Pgrp != syscall.Getpgrp() {
				t.Fatalf("child group %d parent %d", got.Pgrp, syscall.Getpgrp())
			}
		})
	}
}

func TestExitAndSpawnStatus(t *testing.T) {
	for _, mode := range []string{"exec", "run"} {
		for _, n := range []int{0, 1, 2, 42, 126, 127, 130, 255} {
			t.Run(fmt.Sprintf("%s/%d", mode, n), func(t *testing.T) {
				cfg := childConfig(t, "exit", n)
				cfg.Mode = mode
				cmd := wrapperCommand(t, cfg)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				s := waitCommand(t, beginCommandWait(cmd, nil))
				if !s.Exited() || s.ExitStatus() != n {
					t.Fatalf("status %v", s)
				}
			})
		}
		for _, kind := range []string{"missing", "permission", "format", "directory"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				cfg := childConfig(t, "exit", 0)
				cfg.Mode = mode
				cfg.Path = filepath.Join(t.TempDir(), "gh")
				want := 126
				switch kind {
				case "missing":
					want = 127
				case "directory":
					if err := os.Mkdir(cfg.Path, 0700); err != nil {
						t.Fatal(err)
					}
				case "permission":
					if err := os.WriteFile(cfg.Path, []byte("no"), 0600); err != nil {
						t.Fatal(err)
					}
				case "format":
					if err := os.WriteFile(cfg.Path, []byte("not executable format"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				cmd := wrapperCommand(t, cfg)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				s := waitCommand(t, beginCommandWait(cmd, nil))
				if !s.Exited() || s.ExitStatus() != want {
					t.Fatalf("status %v want %d", s, want)
				}
			})
		}
	}
}

func TestSignalWaitStatus(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGKILL, syscall.SIGPIPE, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGSEGV} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd := wrapperCommand(t, childConfig(t, "kill", int(sig)))
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			s := waitCommand(t, beginCommandWait(cmd, nil))
			if !s.Signaled() || s.Signal() != sig {
				t.Fatalf("wait status %v want signaled %v", s, sig)
			}
		})
	}
}

func TestBinaryAndSpoolEndToEnd(t *testing.T) {
	for _, mode := range []string{"exec", "run"} {
		for _, size := range []int{0, 4096, 2 << 20} {
			t.Run(fmt.Sprintf("%s/%d", mode, size), func(t *testing.T) {
				cfg := childConfig(t, "cat", 0)
				cfg.Mode = mode
				cfg.Spool = true
				if mode == "exec" {
					cfg.SpoolLimit = int64(size + 1)
				}
				cmd := wrapperCommand(t, cfg)
				payload := bytes.Repeat([]byte{0, 255, 1, 3}, size/4)
				cmd.Stdin = bytes.NewReader(payload)
				out, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out, payload) {
					t.Fatalf("replay checksum %x want %x", sha256.Sum256(out), sha256.Sum256(payload))
				}
			})
		}
	}
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("binary/capture=%v", capture), func(t *testing.T) {
			cfg := childConfig(t, "binary", 0)
			cfg.Capture = capture
			cmd := wrapperCommand(t, cfg)
			var out, errout bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errout
			if err := cmd.Run(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), []byte{0, 1, 255, 0, 10}) || !bytes.Equal(errout.Bytes(), []byte{255, 0, 2, 10}) {
				t.Fatalf("binary streams %v %v", out.Bytes(), errout.Bytes())
			}
		})
	}
}

func TestLargeOutputAndStreaming(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprint(capture), func(t *testing.T) {
			cfg := childConfig(t, "output", 64<<20)
			cfg.Capture = capture
			cfg.ResultFile = filepath.Join(t.TempDir(), "result")
			cmd := wrapperCommand(t, cfg)
			r, outW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout = outW
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = outW.Close()
			waiter := beginCommandWait(cmd, func() { _ = r.Close() })
			t.Cleanup(func() { cleanupCommand(t, waiter) })
			if err := r.SetReadDeadline(time.Now().Add(commandTimeout)); err != nil {
				t.Fatal(err)
			}
			h := sha256.New()
			n, err := io.Copy(h, r)
			if err != nil {
				t.Fatal(err)
			}
			s := waitCommand(t, waiter)
			want := sha256.New()
			for range 2048 {
				_, _ = want.Write(bytes.Repeat([]byte{0, 1, 2, 255}, 8192))
			}
			if !s.Exited() || s.ExitStatus() != 0 || n != 64<<20 || !bytes.Equal(h.Sum(nil), want.Sum(nil)) {
				t.Fatalf("large output %d status %v checksum %x", n, s, h.Sum(nil))
			}
			if capture {
				b, err := os.ReadFile(cfg.ResultFile)
				if err != nil {
					t.Fatal(err)
				}
				var result struct{ Out *Captured }
				if err = json.Unmarshal(b, &result); err != nil {
					t.Fatal(err)
				}
				if result.Out.Size != 64<<20 || !result.Out.Truncated || !bytes.Equal(result.Out.Data, []byte{0, 1, 2, 255, 0, 1, 2}) {
					t.Fatalf("captured %#v", result.Out)
				}
			}
			cfg = childConfig(t, "stream", 0)
			cfg.Capture = capture
			piped, reader, input := startPiped(t, cfg)
			if lineWithin(t, reader) != "first" {
				t.Fatal("first chunk not streamed")
			}
			_, _ = input.Write([]byte("continue\n"))
			if lineWithin(t, reader) != "second" {
				t.Fatal("second chunk")
			}
			input.Close()
			if s = waitCommand(t, piped); !s.Exited() || s.ExitStatus() != 0 {
				t.Fatal(s)
			}
		})
	}
}

func TestBrokenPipe(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprint(capture), func(t *testing.T) {
			cfg := childConfig(t, "output", 64<<20)
			cfg.Capture = capture
			cmd := wrapperCommand(t, cfg)
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout = w
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waiter := beginCommandWait(cmd, func() { _ = r.Close(); _ = w.Close() })
			t.Cleanup(func() { cleanupCommand(t, waiter) })
			if err := r.SetReadDeadline(time.Now().Add(commandTimeout)); err != nil {
				t.Fatal(err)
			}
			w.Close()
			var b [1]byte
			if _, err = io.ReadFull(r, b[:]); err != nil {
				t.Fatal(err)
			}
			r.Close()
			s := waitCommand(t, waiter)
			if !s.Signaled() || s.Signal() != syscall.SIGPIPE {
				t.Fatalf("status %v want SIGPIPE", s)
			}
		})
	}
}

func TestDirectedSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd, r, _ := startPiped(t, childConfig(t, "signal", int(sig)))
			if !strings.HasPrefix(lineWithin(t, r), "READY ") {
				t.Fatal("not ready")
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if got := lineWithin(t, r); got != "COUNT 1" {
				t.Fatal(got)
			}
			s := waitCommand(t, cmd)
			if !s.Signaled() || s.Signal() != sig {
				t.Fatal(s)
			}
		})
	}
}

func TestInheritedIgnoredSignals(t *testing.T) {
	for _, mode := range []string{"exec", "run"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP} {
			t.Run(mode+"/"+sig.String(), func(t *testing.T) {
				cfg := childConfig(t, "metadata", 0)
				cfg.Mode = mode
				cmd := wrapperCommand(t, cfg)
				cmd.Env = append(cmd.Env, helperKey+"=ignore", "GHOK_IGNORE_SIGNAL="+strconv.Itoa(int(sig)))
				b, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				var got metadata
				if err = json.Unmarshal(b, &got); err != nil {
					t.Fatal(err)
				}
				if !got.Ignored[strconv.Itoa(int(sig))] {
					t.Fatalf("inherited SIG_IGN lost: %s", b)
				}
			})
		}
	}
}

func TestContextCancellationAndGrandchild(t *testing.T) {
	for _, capture := range []bool{false, true} {
		cfg := childConfig(t, "ignoreterm", 0)
		cfg.Cancel = 500 * time.Millisecond
		cfg.Capture = capture
		cmd, r, _ := startPiped(t, cfg)
		ready := lineWithin(t, r)
		fields := strings.Fields(ready)
		pid, _ := strconv.Atoi(fields[1])
		s := waitCommand(t, cmd)
		if !s.Signaled() || s.Signal() != syscall.SIGKILL {
			t.Fatalf("cancel status %v", s)
		}
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("child not reaped: %v", err)
		}
	}
	cfg := childConfig(t, "grandchild", 0)
	cfg.Capture = true
	cmd, r, _ := startPiped(t, cfg)
	ready := lineWithin(t, r)
	pid, _ := strconv.Atoi(strings.Fields(ready)[1])
	_ = cmd.Process.Signal(syscall.SIGTERM)
	if got := lineWithin(t, r); got != "REAPED" {
		t.Fatal(got)
	}
	s := waitCommand(t, cmd)
	if !s.Signaled() || s.Signal() != syscall.SIGTERM {
		t.Fatal(s)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("grandchild left alive: %v", err)
	}
}

func BenchmarkRunTrue(b *testing.B) {
	for _, supervised := range []bool{false, true} {
		b.Run(fmt.Sprint(supervised), func(b *testing.B) {
			for b.Loop() {
				if supervised {
					ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
					r := Run(ctx, Options{Path: "/usr/bin/true", Args: []string{"true"}, Env: []string{}})
					cancel()
					if r.StartErr != nil || r.ExitCode != 0 {
						b.Fatal(r)
					}
				} else {
					ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
					cmd := exec.CommandContext(ctx, "/usr/bin/true")
					cmd.WaitDelay = commandReapTimeout
					cmd.Env = []string{} // Same empty environment as the supervised case.
					err := cmd.Run()
					cancel()
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
