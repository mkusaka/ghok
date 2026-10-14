//go:build unix

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mkusaka/ghok/internal/model"
)

// Spool holds analysis bytes and a byte-exact stdin replay. It never owns source.
// Close must be called even when SpoolStdin returns both a spool and an error.
// Its methods, apart from the returned pump finish function, are not concurrent.
type Spool struct {
	data       model.StdinData
	source     *os.File
	replay     *os.File
	prefix     *os.File
	writer     *os.File
	supervised bool
	probeFIFO  bool
	finish     func()
	closed     bool
}

func (s *Spool) Data() model.StdinData     { return s.data }
func (s *Spool) File() *os.File            { return s.replay }
func (s *Spool) RequiresSupervision() bool { return s.supervised }

// SpoolStdin leaves terminals untouched, restores regular-file offsets, and
// captures streams in an immediately unlinked, private temporary file. A stream
// whose EOF is not captured needs a supervised pump to replay its consumed prefix.
func SpoolStdin(f *os.File, maxBytes int64, timeout time.Duration) (*Spool, error) {
	s := &Spool{source: f, replay: f}
	fail := func(err error) (*Spool, error) {
		s.data = model.StdinData{Status: model.StdinError, Error: err.Error()}
		return s, err
	}
	if f == nil {
		return fail(errors.New("stdin is nil"))
	}
	if maxBytes < 0 {
		return fail(errors.New("stdin capture limit must be nonnegative"))
	}
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if isTerminal(f) {
		s.data.Status = model.StdinTTY
		return s, nil
	}
	if info.Mode().IsRegular() {
		offset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return fail(err)
		}
		var total int64
		buf := make([]byte, 32*1024)
		for {
			n, readErr := f.Read(spoolReadBuffer(buf, maxBytes, total))
			total += int64(n)
			if total <= maxBytes {
				s.data.Data = append(s.data.Data, buf[:n]...)
			}
			if total > maxBytes {
				s.data = model.StdinData{Status: model.StdinTooLarge}
				break
			}
			if readErr != nil {
				if readErr == io.EOF {
					s.data.Status = model.StdinCaptured
				} else {
					err = readErr
				}
				break
			}
		}
		_, seekErr := f.Seek(offset, io.SeekStart)
		if err = errors.Join(err, seekErr); err != nil {
			return fail(err)
		}
		return s, nil
	}

	prefix, err := os.CreateTemp("", "ghok-stdin-*")
	if err != nil {
		return fail(err)
	}
	if err = os.Remove(prefix.Name()); err != nil {
		_ = prefix.Close()
		return fail(err)
	}
	s.prefix = prefix
	fd := int(f.Fd())
	s.probeFIFO = spoolProbeFIFO && info.Mode()&os.ModeNamedPipe != 0
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var total int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := spoolReadStream(fd, -1, spoolReadBuffer(buf, maxBytes, total), deadline, s.probeFIFO)
		if errors.Is(readErr, os.ErrDeadlineExceeded) {
			s.data = model.StdinData{Status: model.StdinTimeout}
			break
		}
		if n > 0 {
			// Persist every consumed byte, including the byte proving too-large.
			if _, writeErr := prefix.Write(buf[:n]); writeErr != nil {
				_ = s.Close()
				return nil, fmt.Errorf("persist stdin replay: %w", writeErr)
			}
			total += int64(n)
			if total > maxBytes {
				s.data = model.StdinData{Status: model.StdinTooLarge}
				break
			}
			s.data.Data = append(s.data.Data, buf[:n]...)
		}
		if readErr != nil {
			err = readErr
			break
		}
		if n == 0 {
			s.data.Status = model.StdinCaptured
			break
		}
	}
	if _, seekErr := prefix.Seek(0, io.SeekStart); seekErr != nil {
		_ = s.Close()
		return nil, errors.Join(err, seekErr)
	}
	if s.data.Status == model.StdinCaptured {
		s.replay = prefix
		return s, nil
	}
	// A read error may follow a valid prefix: retain it for the caller's child.
	readErr := err
	s.replay, s.writer, err = os.Pipe()
	err = errors.Join(readErr, err)
	if s.replay == nil {
		_ = s.Close()
		return nil, err
	}
	s.supervised = true
	if err != nil {
		return fail(err)
	}
	return s, nil
}

// Read at most one byte beyond the analysis limit without overflowing maxBytes.
func spoolReadBuffer(buf []byte, maxBytes, total int64) []byte {
	remaining := maxBytes - total
	if remaining < int64(len(buf)) {
		return buf[:int(remaining)+1]
	}
	return buf
}

// startPump must be called only after spawning the child. finish cancels both
// an idle source read and a blocked replay write, waits, and closes pump fds.
func (s *Spool) startPump() (func(), error) {
	if s.closed {
		return nil, os.ErrClosed
	}
	if !s.supervised {
		return func() {}, nil
	}
	if s.finish != nil {
		return nil, errors.New("stdin replay pump already started")
	}
	cancelRead, cancelWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cancelFD := int(cancelRead.Fd())
	sourceFD := int(s.source.Fd())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = s.writer.Close() }()
		buf := make([]byte, 32*1024)
		for {
			n, err := s.prefix.Read(buf)
			if n > 0 {
				if _, writeErr := s.writer.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					return
				}
				break
			}
		}
		for {
			n, err := spoolReadStream(sourceFD, cancelFD, buf, time.Time{}, s.probeFIFO)
			if n > 0 {
				if _, writeErr := s.writer.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil || n == 0 {
				return
			}
		}
	}()
	var once sync.Once
	s.finish = func() {
		once.Do(func() {
			_ = cancelWrite.Close()
			_ = s.writer.SetWriteDeadline(time.Now())
			<-done
			_ = cancelRead.Close()
			_ = s.prefix.Close()
			_ = s.replay.Close()
			s.replay = nil
			s.prefix = nil
			s.writer = nil
		})
	}
	return s.finish, nil
}

func (s *Spool) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	if s.finish != nil {
		s.finish()
	}
	var err error
	if s.writer != nil {
		err = errors.Join(err, s.writer.Close())
		s.writer = nil
	}
	if s.prefix != nil {
		err = errors.Join(err, s.prefix.Close())
		if s.replay == s.prefix {
			s.replay = nil
		}
		s.prefix = nil
	}
	if s.replay != nil && s.replay != s.source {
		err = errors.Join(err, s.replay.Close())
		s.replay = nil
	}
	return err
}

// pollfd is the POSIX poll(2) layout on macOS/Linux amd64/arm64.
type spoolPollFD struct {
	fd      int32
	events  int16
	revents int16
}

// A raw readiness wait works for redirected os.Stdin too: unlike os.Pipe's
// runtime-pollable descriptors, inherited blocking descriptors reject deadlines.
// The caller must be the sole reader. Ordinary streams are read after readiness;
// Darwin FIFOs additionally need nonblocking EOF probes in spoolReadStream.
func spoolWaitReadable(source, cancel int, deadline time.Time) (bool, error) {
	fds := [2]spoolPollFD{{fd: int32(source), events: 1}, {fd: int32(cancel), events: 1}}
	for {
		timeout := time.Duration(-1)
		if !deadline.IsZero() {
			timeout = time.Until(deadline)
			if timeout <= 0 {
				return false, nil
			}
		}
		err := spoolPoll(&fds, timeout)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if fds[1].revents != 0 {
			return false, io.EOF
		}
		if fds[0].revents&32 != 0 { // POLLNVAL
			return false, syscall.EBADF
		}
		if fds[0].revents != 0 {
			return true, nil
		}
		// A very long deadline may exceed poll's millisecond range.
		if deadline.IsZero() || time.Now().Before(deadline) {
			continue
		}
		return false, nil
	}
}

// Darwin FIFO readiness can omit EOF after the last writer closes. Probe with
// O_NONBLOCK after bounded waits so both capture and the live pump see EOF.
// Restore flags immediately: duplicates share them with the caller's descriptor.
func spoolReadStream(source, cancel int, buf []byte, deadline time.Time, probeFIFO bool) (int, error) {
	for {
		waitDeadline := deadline
		if probeFIFO {
			probeDeadline := time.Now().Add(20 * time.Millisecond)
			if waitDeadline.IsZero() || probeDeadline.Before(waitDeadline) {
				waitDeadline = probeDeadline
			}
		}
		ready, err := spoolWaitReadable(source, cancel, waitDeadline)
		if err != nil {
			return 0, err
		}
		if !ready && !probeFIFO {
			return 0, os.ErrDeadlineExceeded
		}
		var n int
		if probeFIFO {
			n, err = spoolReadNonblocking(source, buf)
		} else {
			n, err = syscall.Read(source, buf)
		}
		if err != syscall.EAGAIN && err != syscall.EINTR {
			return n, err
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, os.ErrDeadlineExceeded
		}
	}
}

func spoolReadNonblocking(fd int, buf []byte) (int, error) {
	flags, err := spoolFcntl(fd, syscall.F_GETFL, 0)
	if err != nil {
		return 0, err
	}
	if flags&syscall.O_NONBLOCK != 0 {
		return syscall.Read(fd, buf)
	}
	if _, err := spoolFcntl(fd, syscall.F_SETFL, flags|syscall.O_NONBLOCK); err != nil {
		return 0, err
	}
	n, readErr := syscall.Read(fd, buf)
	_, restoreErr := spoolFcntl(fd, syscall.F_SETFL, flags)
	if restoreErr != nil {
		return n, errors.Join(readErr, restoreErr)
	}
	return n, readErr
}

func spoolFcntl(fd, cmd int, arg uintptr) (uintptr, error) {
	for {
		value, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), arg)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, errno
		}
		return value, nil
	}
}
