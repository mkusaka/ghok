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

	"github.com/mkusaka/ghok/internal/model"
)

func spoolTestPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

func spoolTestClose(t *testing.T, s *Spool) {
	t.Helper()
	for range 2 {
		done := make(chan error, 1)
		go func() { done <- s.Close() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(commandReapTimeout):
			t.Fatal("spool Close did not finish")
		}
	}
}

func spoolTestFinish(t *testing.T, finish func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { finish(); close(done) }()
	select {
	case <-done:
	case <-time.After(commandReapTimeout):
		t.Fatal("spool pump did not finish")
	}
}

func spoolTestReplay(t *testing.T, s *Spool, want []byte) {
	t.Helper()
	if s.RequiresSupervision() {
		finish, err := s.startPump()
		if err != nil {
			t.Fatal(err)
		}
		defer spoolTestFinish(t, finish)
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(s.File())
		done <- result{data, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || !bytes.Equal(got.data, want) {
			t.Fatalf("replay: got %d bytes, err %v; want %d identical bytes", len(got.data), got.err, len(want))
		}
	case <-time.After(5 * time.Second):
		spoolTestClose(t, s)
		t.Fatal("replay did not finish")
	}
}

func TestSpoolStreamCaptured(t *testing.T) {
	for _, payload := range [][]byte{nil, {0}, {0, 255, 'x', 0, '\n'}} {
		t.Run(string([]byte{byte(len(payload) + '0')}), func(t *testing.T) {
			r, w := spoolTestPipe(t)
			if _, err := w.Write(payload); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			s, err := SpoolStdin(r, int64(len(payload)), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer spoolTestClose(t, s)
			if s.Data().Status != model.StdinCaptured || !bytes.Equal(s.Data().Data, payload) || s.RequiresSupervision() {
				t.Fatalf("unexpected capture: %+v, supervised=%v", s.Data(), s.RequiresSupervision())
			}
			spoolTestReplay(t, s, payload)
			if _, err := r.Stat(); err != nil {
				t.Fatalf("caller stdin closed: %v", err)
			}
		})
	}
}

func TestSpoolRegularOffsetAndLimit(t *testing.T) {
	payload := []byte{'p', 'r', 'e', 0, 1, 255, 'z'}
	for _, limit := range []int64{0, 3, 4, 100} {
		t.Run(string([]byte{byte(limit + 'a')}), func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "input")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err = f.Write(payload); err != nil {
				t.Fatal(err)
			}
			if _, err = f.Seek(3, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			s, err := SpoolStdin(f, limit, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer spoolTestClose(t, s)
			if s.File() != f || s.RequiresSupervision() {
				t.Fatal("regular file should replay directly")
			}
			offset, err := f.Seek(0, io.SeekCurrent)
			if err != nil || offset != 3 {
				t.Fatalf("offset=%d err=%v", offset, err)
			}
			if limit < 4 {
				if s.Data().Status != model.StdinTooLarge || s.Data().Data != nil {
					t.Fatalf("data: %+v", s.Data())
				}
			} else if s.Data().Status != model.StdinCaptured || !bytes.Equal(s.Data().Data, payload[3:]) {
				t.Fatalf("data: %+v", s.Data())
			}
			spoolTestReplay(t, s, payload[3:])
			spoolTestClose(t, s)
			if _, err := f.Stat(); err != nil {
				t.Fatalf("closed caller file: %v", err)
			}
		})
	}
}

func TestSpoolTooLargeBlockingProducer(t *testing.T) {
	r, w := spoolTestPipe(t)
	payload := bytes.Repeat([]byte{0, 255, 'x', '\n'}, 256*1024)
	written := make(chan error, 1)
	go func() {
		_, err := w.Write(payload)
		_ = w.Close()
		written <- err
	}()
	s, err := SpoolStdin(r, 33, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	if s.Data().Status != model.StdinTooLarge || s.Data().Data != nil || !s.RequiresSupervision() {
		t.Fatalf("unexpected data: %+v supervised=%v", s.Data(), s.RequiresSupervision())
	}
	info, err := s.prefix.Stat()
	if err != nil || info.Size() != 34 {
		t.Fatalf("consumed prefix: %v, err=%v", info, err)
	}
	spoolTestReplay(t, s, payload)
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("producer still blocked")
	}
}

func TestSpoolTimeoutPrefixAndLiveRest(t *testing.T) {
	r, w := spoolTestPipe(t)
	prefix := []byte{0, 1, 255}
	if _, err := w.Write(prefix); err != nil {
		t.Fatal(err)
	}
	s, err := SpoolStdin(r, 100, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	if s.Data().Status != model.StdinTimeout || s.Data().Data != nil || !s.RequiresSupervision() {
		t.Fatalf("unexpected data: %+v supervised=%v", s.Data(), s.RequiresSupervision())
	}
	rest := []byte{'l', 'i', 'v', 'e', 0}
	if _, err := w.Write(rest); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	spoolTestReplay(t, s, append(prefix, rest...))
}

func TestSpoolInheritedBlockingDescriptorTimeout(t *testing.T) {
	r, w := spoolTestPipe(t)
	// NewFile around a blocking duplicate models redirected os.Stdin, unlike
	// os.Pipe's runtime-pollable read end. Dup shares flags, so Fd first.
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(r.Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(fd), "inherited-stdin")
	defer input.Close()
	if err := input.SetReadDeadline(time.Now()); !errors.Is(err, os.ErrNoDeadline) {
		t.Fatalf("expected non-pollable inherited descriptor, got %v", err)
	}
	start := time.Now()
	s, err := SpoolStdin(input, 1024, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
	if s.Data().Status != model.StdinTimeout {
		t.Fatalf("status=%s", s.Data().Status)
	}
	if _, err := w.Write([]byte{42}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	spoolTestReplay(t, s, []byte{42})
}

func TestSpoolPumpCancellation(t *testing.T) {
	for _, blockedWrite := range []bool{false, true} {
		name := "idle-source"
		if blockedWrite {
			name = "blocked-child-write"
		}
		t.Run(name, func(t *testing.T) {
			r, w := spoolTestPipe(t)
			var producer chan error
			if blockedWrite {
				producer = make(chan error, 1)
				go func() { _, err := w.Write(bytes.Repeat([]byte{1}, 2*1024*1024)); producer <- err }()
			}
			limit := int64(0)
			if blockedWrite {
				limit = 1024 * 1024
			}
			timeout := 50 * time.Millisecond
			if blockedWrite {
				timeout = 5 * time.Second
			}
			s, err := SpoolStdin(r, limit, timeout)
			if err != nil {
				t.Fatal(err)
			}
			defer spoolTestClose(t, s)
			finish, err := s.startPump()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.startPump(); err == nil {
				t.Fatal("second pump unexpectedly started")
			}
			if !blockedWrite {
				// Receiving this byte proves the pump advanced to the live stream.
				if _, err := w.Write([]byte{7}); err != nil {
					t.Fatal(err)
				}
			}
			// A first byte proves the pump began writing. With a large prefix,
			// the remaining data cannot fit in the unread child pipe.
			var one [1]byte
			if _, err := io.ReadFull(s.File(), one[:]); err != nil {
				t.Fatal(err)
			}
			finished := make(chan struct{})
			go func() { finish(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("pump cancellation did not finish")
			}
			spoolTestFinish(t, finish)
			if _, err := r.Stat(); err != nil {
				t.Fatalf("caller source closed: %v", err)
			}
			if producer != nil {
				_ = r.Close()
				select {
				case <-producer:
				case <-time.After(5 * time.Second):
					t.Fatal("producer cleanup did not finish")
				}
			}
		})
	}
}

func TestSpoolTempPrivateAndUnlinked(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, captured := range []bool{true, false} {
		r, w := spoolTestPipe(t)
		_, _ = w.Write([]byte{0, 2, 3})
		if captured {
			_ = w.Close()
		}
		limit := int64(10)
		if !captured {
			limit = 1
		}
		s, err := SpoolStdin(r, limit, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		info, err := s.prefix.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("temp permissions=%v", info.Mode().Perm())
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Nlink != 0 {
			t.Fatal("temp file remains linked")
		}
		assertEmpty := func() {
			entries, err := os.ReadDir(os.Getenv("TMPDIR"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary files remain: %v err=%v", entries, err)
			}
		}
		assertEmpty()
		spoolTestClose(t, s)
		assertEmpty()
		if _, err := r.Stat(); err != nil {
			t.Fatalf("caller source closed: %v", err)
		}
	}
}

func TestSpoolReadErrorAndInvalidArguments(t *testing.T) {
	if s, err := SpoolStdin(nil, 10, 0); err == nil || s.Data().Status != model.StdinError {
		t.Fatalf("nil input: spool=%v err=%v", s, err)
	}
	r, w := spoolTestPipe(t)
	s, err := SpoolStdin(r, -1, 0)
	if err == nil || s.Data().Status != model.StdinError {
		t.Fatalf("negative limit: data=%+v err=%v", s.Data(), err)
	}
	spoolTestClose(t, s)
	_, _ = w.Write([]byte{9})
	_ = w.Close()
	s, err = SpoolStdin(r, 10, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	spoolTestReplay(t, s, []byte{9})
	spoolTestClose(t, s)

	f, err := os.OpenFile(t.TempDir()+"/write-only", os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.Write([]byte{1, 2})
	_, _ = f.Seek(1, io.SeekStart)
	s, err = SpoolStdin(f, 10, 0)
	if err == nil || s.Data().Status != model.StdinError || s.Data().Error == "" {
		t.Fatalf("read error: data=%+v err=%v", s.Data(), err)
	}
	offset, seekErr := f.Seek(0, io.SeekCurrent)
	if seekErr != nil || offset != 1 {
		t.Fatalf("error changed offset=%d err=%v", offset, seekErr)
	}
	spoolTestClose(t, s)
	_ = f.Close()
	s, err = SpoolStdin(f, 10, 0)
	if err == nil || s.Data().Status != model.StdinError {
		t.Fatalf("closed input: %+v err=%v", s.Data(), err)
	}
	spoolTestClose(t, s)
}

func TestSpoolSocketReplay(t *testing.T) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	r := os.NewFile(uintptr(fds[0]), "socket-input")
	w := os.NewFile(uintptr(fds[1]), "socket-producer")
	defer r.Close()
	defer w.Close()
	payload := []byte{0, 255, 42, 0}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Shutdown(fds[1], syscall.SHUT_WR); err != nil {
		t.Fatal(err)
	}
	s, err := SpoolStdin(r, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	if s.Data().Status != model.StdinTooLarge {
		t.Fatalf("status=%s", s.Data().Status)
	}
	spoolTestReplay(t, s, payload)
}

func TestSpoolDescriptorsCloseOnExec(t *testing.T) {
	r, w := spoolTestPipe(t)
	_, _ = w.Write([]byte{1, 2})
	s, err := SpoolStdin(r, 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	for _, f := range []*os.File{s.prefix, s.replay, s.writer} {
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFD, 0)
		if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
			t.Fatalf("fd %s not CLOEXEC: flags=%d err=%v", f.Name(), flags, errno)
		}
	}
}

func TestSpoolStreamReadErrorUsableReplay(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := SpoolStdin(f, 10, time.Second)
	if err == nil || s == nil || s.Data().Status != model.StdinError || s.Data().Error == "" {
		t.Fatalf("directory read: spool=%v err=%v", s, err)
	}
	defer spoolTestClose(t, s)
	if !s.RequiresSupervision() {
		t.Fatal("stream read error should retain a replay pump")
	}
	spoolTestReplay(t, s, nil)
}

func TestSpoolPumpClosedChild(t *testing.T) {
	r, w := spoolTestPipe(t)
	_, _ = w.Write([]byte{0, 1, 2})
	s, err := SpoolStdin(r, 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	// Removing the only reader models a child that closes stdin immediately.
	if err := s.File().Close(); err != nil {
		t.Fatal(err)
	}
	finish, err := s.startPump()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { finish(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pump stuck after child closed stdin")
	}
	if _, err := r.Stat(); err != nil {
		t.Fatalf("caller input closed: %v", err)
	}
}

func TestSpoolTempCreationFailureDoesNotConsume(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	r, w := spoolTestPipe(t)
	payload := []byte{0, 42, 255}
	_, _ = w.Write(payload)
	_ = w.Close()
	s, err := SpoolStdin(r, 10, time.Second)
	if err == nil || s == nil || s.Data().Status != model.StdinError {
		t.Fatalf("temporary file failure: spool=%v err=%v", s, err)
	}
	defer spoolTestClose(t, s)
	if s.File() != r || s.RequiresSupervision() {
		t.Fatal("unconsumed stdin should remain directly replayable")
	}
	spoolTestReplay(t, s, payload)
}

func TestSpoolNamedFIFOEOF(t *testing.T) {
	path := t.TempDir() + "/stdin.fifo"
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 255, 1}
	done := make(chan error, 1)
	go func() {
		w, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			done <- err
			return
		}
		_, err = w.Write(payload)
		err = errors.Join(err, w.Close())
		done <- err
	}()
	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Make writer closure an observed prerequisite, not a scheduling assumption.
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO producer did not write and close")
	}
	s, err := SpoolStdin(r, int64(len(payload)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	if s.Data().Status != model.StdinCaptured || s.RequiresSupervision() {
		t.Fatalf("FIFO EOF status=%s supervised=%v", s.Data().Status, s.RequiresSupervision())
	}
	spoolTestReplay(t, s, payload)
}

func TestSpoolNamedFIFOPumpEOF(t *testing.T) {
	path := t.TempDir() + "/stdin.fifo"
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 255, 1, 42}
	done := make(chan error, 1)
	go func() {
		w, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			done <- err
			return
		}
		_, err = w.Write(payload)
		err = errors.Join(err, w.Close())
		done <- err
	}()
	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO producer did not write and close")
	}
	before, err := spoolFcntl(int(r.Fd()), syscall.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	s, err := SpoolStdin(r, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolTestClose(t, s)
	if s.Data().Status != model.StdinTooLarge || !s.RequiresSupervision() {
		t.Fatalf("FIFO status=%s supervised=%v", s.Data().Status, s.RequiresSupervision())
	}
	spoolTestReplay(t, s, payload)
	after, err := spoolFcntl(int(r.Fd()), syscall.F_GETFL, 0)
	if err != nil || before != after {
		t.Fatalf("FIFO flags changed: before=%d after=%d err=%v", before, after, err)
	}
}
