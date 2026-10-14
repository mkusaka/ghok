//go:build unix

package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func openFDCount() int {
	n := 0
	for fd := 3; fd < 512; fd++ {
		_, _, err := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if err == 0 {
			n++
		}
	}
	return n
}

func TestRunAndExecFailureCleanup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, mode := range []string{"exec", "run"} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte{0, 255, 10}); err != nil {
			t.Fatal(err)
		}
		w.Close()
		spool, err := SpoolStdin(r, 100, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		opts := Options{Path: filepath.Join(tmp, "missing"), Args: []string{"gh"}, Stdin: spool, Capture: mode == "run"}
		if mode == "exec" {
			if err = Exec(opts); !errors.Is(err, syscall.ENOENT) {
				t.Fatal(err)
			}
		} else {
			result := Run(context.Background(), opts)
			if !errors.Is(result.StartErr, syscall.ENOENT) {
				t.Fatal(result.StartErr)
			}
		}
		r.Close()
		entries, err := os.ReadDir(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("spool leftovers %v", entries)
		}
	}
}

func TestRunNoOwnedLeaks(t *testing.T) {
	// Warm runtime signal/poll workers before taking the lifecycle baseline.
	opts := Options{Path: "/usr/bin/true", Args: []string{"true"}, Capture: true, CaptureLimit: 10}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	result := Run(ctx, opts)
	cancel()
	if result.StartErr != nil || result.ExitCode != 0 {
		t.Fatal(result)
	}
	fdBefore, gBefore := openFDCount(), runtime.NumGoroutine()
	for range 16 {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		result = Run(ctx, opts)
		cancel()
		if result.StartErr != nil || result.WaitErr != nil || result.ExitCode != 0 {
			t.Fatal(result)
		}
	}
	if fdAfter := openFDCount(); fdAfter != fdBefore {
		t.Fatalf("open fds before %d after %d", fdBefore, fdAfter)
	}
	// Wait goroutines are joined through their last event; allow that sender
	// to return before checking for an accumulation, without a timing sleep.
	runtime.Gosched()
	if gAfter := runtime.NumGoroutine(); gAfter > gBefore+1 {
		t.Fatalf("goroutines before %d after %d", gBefore, gAfter)
	}
}

func TestExecRefusesLivePumpAndRestoresStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	_, _ = w.Write([]byte{0, 1, 2})
	spool, err := SpoolStdin(r, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !spool.RequiresSupervision() {
		t.Fatal("expected pump")
	}
	if err = Exec(Options{Path: "/usr/bin/true", Args: []string{"true"}, Stdin: spool}); !errors.Is(err, syscall.EINVAL) {
		t.Fatal(err)
	}
	// Failure restoration must preserve fd 0 even when replaced by replay.
	var original, after syscall.Stat_t
	if err = syscall.Fstat(0, &original); err != nil {
		t.Fatal(err)
	}
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	_, _ = w2.Write([]byte{4, 0, 5})
	w2.Close()
	spool, err = SpoolStdin(r2, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = Exec(Options{Path: "/nonexistent-ghok-test/gh", Args: []string{"gh"}, Stdin: spool}); !errors.Is(err, syscall.ENOENT) {
		t.Fatal(err)
	}
	if err = syscall.Fstat(0, &after); err != nil {
		t.Fatal(err)
	}
	if original.Dev != after.Dev || original.Ino != after.Ino {
		t.Fatal("exec failure changed fd 0")
	}
}

func TestPipeSpoolTimeoutRunReplay(t *testing.T) {
	cfg := childConfig(t, "cat", 0)
	cfg.Spool = true
	cfg.Timeout = 100 * time.Millisecond
	cfg.SpoolLimit = 100
	cmd, r, w := startPiped(t, cfg)
	payload := []byte{0, 255, 1, 3, 10}
	_, _ = w.Write(payload)
	got := make([]byte, len(payload))
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(r, got); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout replay did not stream prefix")
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("prefix %v", got)
	}
	_, _ = w.Write([]byte("rest\n"))
	w.Close()
	if lineWithin(t, r) != "rest" {
		t.Fatal("rest lost")
	}
	if status := waitCommand(t, cmd); !status.Exited() || status.ExitStatus() != 0 {
		t.Fatal(status)
	}
}
