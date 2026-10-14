//go:build unix

package process

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// These tests deliberately do not use t.Parallel: capture tees to the original
// global stdout/stderr, just as Run does.
func captureTestOutputs(t *testing.T, shared bool) (outR, outW, errR, errW *os.File) {
	t.Helper()
	var err error
	outR, outW, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err = os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	if shared {
		os.Stderr = outW
	}
	t.Cleanup(func() {
		os.Stdout, os.Stderr = oldOut, oldErr
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
	})
	return
}

func captureTestWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not terminate")
	}
}

func captureTestClose(t *testing.T, c *capture) {
	t.Helper()
	done := make(chan struct{})
	go func() { c.close(); close(done) }()
	captureTestWait(t, done)
}

func captureTestFinish(t *testing.T, c *capture) (out, stderr *Captured) {
	t.Helper()
	done := make(chan struct{})
	go func() { out, stderr = c.finish(); close(done) }()
	captureTestWait(t, done)
	return out, stderr
}

func captureTestFlags(t *testing.T, fd uintptr) int {
	t.Helper()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	return int(flags)
}

func TestCaptureBoundedBinaryTee(t *testing.T) {
	payload := []byte{'a', 0, 255, '\n', 'z'}
	for _, limit := range []int64{-1, 0, 3, 5, 20} {
		t.Run(time.Duration(limit).String(), func(t *testing.T) {
			outR, outW, errR, errW := captureTestOutputs(t, false)
			c, err := startCapture(limit)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { captureTestClose(t, c) })
			var gotOut, gotErr []byte
			readDone := make(chan struct{})
			go func() {
				gotOut, _ = io.ReadAll(outR)
				gotErr, _ = io.ReadAll(errR)
				close(readDone)
			}()
			if _, err := c.stdout.Write(payload); err != nil {
				t.Fatal(err)
			}
			if _, err := c.stderr.Write(payload); err != nil {
				t.Fatal(err)
			}
			c.closeWrites()
			out, stderr := captureTestFinish(t, c)
			if err := outW.Close(); err != nil {
				t.Fatal(err)
			}
			if err := errW.Close(); err != nil {
				t.Fatal(err)
			}
			captureTestWait(t, readDone)
			if !bytes.Equal(gotOut, payload) || !bytes.Equal(gotErr, payload) {
				t.Fatalf("tee output: stdout=%q stderr=%q", gotOut, gotErr)
			}
			keep := min(max(limit, 0), int64(len(payload)))
			for _, got := range []*Captured{out, stderr} {
				if !bytes.Equal(got.Data, payload[:int(keep)]) || got.Size != int64(len(payload)) || got.Truncated != (keep < int64(len(payload))) || got.Error != nil {
					t.Fatalf("capture(limit=%d) = %+v", limit, got)
				}
			}
		})
	}
}

func TestCaptureEmpty(t *testing.T) {
	captureTestOutputs(t, false)
	c, err := startCapture(1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { captureTestClose(t, c) })
	c.closeWrites()
	out, stderr := captureTestFinish(t, c)
	for _, got := range []*Captured{out, stderr} {
		if len(got.Data) != 0 || got.Size != 0 || got.Truncated || got.Error != nil {
			t.Fatalf("empty capture = %+v", got)
		}
	}
}

func TestCaptureStreamsBeforeFinish(t *testing.T) {
	outR, _, _, _ := captureTestOutputs(t, false)
	c, err := startCapture(1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { captureTestClose(t, c) })
	if _, err := c.stdout.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := outR.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len("first\n"))
	if _, err := io.ReadFull(outR, first); err != nil || string(first) != "first\n" {
		t.Fatalf("first chunk before finish: %q, %v", first, err)
	}
	if _, err := c.stdout.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	c.closeWrites()
	out, _ := captureTestFinish(t, c)
	second := make([]byte, len("second\n"))
	if _, err := io.ReadFull(outR, second); err != nil || string(second) != "second\n" {
		t.Fatalf("second chunk: %q, %v", second, err)
	}
	if string(out.Data) != "first\nsecond\n" || out.Error != nil {
		t.Fatalf("stream capture = %+v", out)
	}
}

func TestCaptureBrokenPipeClosesChildReadEnd(t *testing.T) {
	outR, _, _, _ := captureTestOutputs(t, false)
	if err := outR.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := startCapture(1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { captureTestClose(t, c) })
	if _, err := c.stdout.Write([]byte("broken")); err != nil {
		t.Fatal(err)
	}
	captureTestWait(t, c.out.done)
	if _, err := c.stdout.Write([]byte("again")); !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("child write after tee failure = %v; want EPIPE", err)
	}
	c.closeWrites()
	out, _ := captureTestFinish(t, c)
	if !errors.Is(out.Error, syscall.EPIPE) || string(out.Data) != "broken" {
		t.Fatalf("broken pipe capture = %+v", out)
	}
}

func TestCaptureRestoresSharedOutputFlags(t *testing.T) {
	for _, abort := range []bool{false, true} {
		name := "finish"
		if abort {
			name = "spawn-failure-close"
		}
		t.Run(name, func(t *testing.T) {
			_, outW, _, _ := captureTestOutputs(t, true)
			fd := outW.Fd()
			before := captureTestFlags(t, fd)
			c, err := startCapture(20)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { captureTestClose(t, c) })
			for _, f := range []*os.File{c.stdout, c.stderr, c.out.read, c.err.read, c.out.sink.file, c.err.sink.file} {
				raw, err := f.SyscallConn()
				if err != nil {
					t.Fatal(err)
				}
				err = raw.Control(func(fd uintptr) {
					flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
					if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
						t.Errorf("capture fd is not CLOEXEC: flags=%d, err=%v", flags, errno)
					}
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if abort {
				captureTestClose(t, c)
			} else {
				c.closeWrites()
				captureTestFinish(t, c)
			}
			if after := captureTestFlags(t, fd); after != before {
				t.Fatalf("output status flags: before=%#x after=%#x", before, after)
			}
			captureTestClose(t, c)
		})
	}
}

func TestCaptureCloseCancelsFinishWithRetainedWriter(t *testing.T) {
	captureTestOutputs(t, false)
	c, err := startCapture(20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { captureTestClose(t, c) })
	fd, _, errno := syscall.Syscall(syscall.SYS_FCNTL, c.stdout.Fd(), syscall.F_DUPFD_CLOEXEC, 3)
	if errno != 0 {
		t.Fatal(errno)
	}
	defer func() { _ = syscall.Close(int(fd)) }()
	c.closeWrites()
	done := make(chan struct{})
	var out *Captured
	go func() {
		out, _ = c.finish()
		close(done)
	}()
	captureTestClose(t, c)
	captureTestWait(t, done)
	if !errors.Is(out.Error, os.ErrDeadlineExceeded) {
		t.Fatalf("retained child writer error = %v", out.Error)
	}
}

func TestCaptureAbortUnblocksFullDownstream(t *testing.T) {
	_, outW, _, _ := captureTestOutputs(t, false)
	fd := outW.Fd()
	if err := syscall.SetNonblock(int(fd), true); err != nil {
		t.Fatal(err)
	}
	// Fill the downstream pipe without a reader, using raw nonblocking writes
	// rather than os.File.Write (which would wait in Go's runtime poller).
	block := make([]byte, 4096)
	for {
		_, err := syscall.Write(int(fd), block)
		if errors.Is(err, syscall.EAGAIN) {
			break
		}
		if err != nil && !errors.Is(err, syscall.EINTR) {
			t.Fatal(err)
		}
	}
	c, err := startCapture(20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { captureTestClose(t, c) })
	if _, err := c.stdout.Write([]byte("blocked")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		c.close()
		close(done)
	}()
	captureTestWait(t, done)
	captureTestWait(t, c.out.done)
	captureTestWait(t, c.err.done)
	if after := captureTestFlags(t, fd); after&syscall.O_NONBLOCK == 0 {
		t.Fatalf("abort did not preserve initially nonblocking downstream: %#x", after)
	}
}

func TestCaptureStartFailureClosesOwnedDescriptors(t *testing.T) {
	_, outW, _, errW := captureTestOutputs(t, false)
	fd := outW.Fd()
	before := captureTestFlags(t, fd)
	if err := errW.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := startCapture(20)
	if err == nil || c != nil {
		t.Fatalf("capture with closed stderr = %v, %v", c, err)
	}
	if after := captureTestFlags(t, fd); after != before {
		t.Fatalf("failed setup changed stdout flags: before=%#x after=%#x", before, after)
	}
}
