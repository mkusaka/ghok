//go:build unix

package process

import (
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// Captured contains the bounded prefix and total number of bytes observed on a
// stream. Error reports a read, tee, or drain error without changing child status.
type Captured struct {
	Data      []byte
	Size      int64
	Truncated bool
	Error     error
}

type capture struct {
	stdout, stderr *os.File
	out, err       *captureStream
	writesOnce     sync.Once
	abortOnce      sync.Once
	cleanupOnce    sync.Once
}

type captureStream struct {
	read   *os.File
	sink   captureSink
	result Captured
	done   chan struct{}
}

type captureSink struct {
	fd      int
	flags   int
	changed bool
	file    *os.File
}

func startCapture(limit int64) (*capture, error) {
	if limit < 0 {
		limit = 0
	}
	c := &capture{out: &captureStream{}, err: &captureStream{}}
	c.out.sink.fd = -1
	c.err.sink.fd = -1
	var err error
	// Snapshot BOTH status flag sets before changing either: stdout and stderr
	// can refer to the same open-file description (for example, `2>&1`).
	c.out.sink, err = duplicateCaptureSink(os.Stdout)
	if err == nil {
		c.err.sink, err = duplicateCaptureSink(os.Stderr)
	}
	if err == nil {
		err = c.out.sink.prepare()
	}
	if err == nil {
		err = c.err.sink.prepare()
	}
	if err == nil {
		c.out.read, c.stdout, err = os.Pipe()
	}
	if err == nil {
		c.err.read, c.stderr, err = os.Pipe()
	}
	if err != nil {
		c.closeWrites()
		if c.out.read != nil {
			_ = c.out.read.Close()
		}
		if c.err.read != nil {
			_ = c.err.read.Close()
		}
		c.closeSinks()
		return nil, err
	}
	for _, stream := range []*captureStream{c.out, c.err} {
		stream.done = make(chan struct{})
		go stream.pump(limit)
	}
	return c, nil
}

func duplicateCaptureSink(original *os.File) (captureSink, error) {
	sink := captureSink{fd: -1}
	// An fd above 2 avoids os.File.Write's special SIGPIPE handling for stdout
	// and stderr: the wrapper must survive EPIPE to report the CHILD's status.
	raw, err := original.SyscallConn()
	if err != nil {
		return sink, err
	}
	var dupErr error
	err = raw.Control(func(originalFD uintptr) {
		fd, _, errno := syscall.Syscall(syscall.SYS_FCNTL, originalFD, syscall.F_DUPFD_CLOEXEC, 3)
		if errno != 0 {
			dupErr = os.NewSyscallError("fcntl(F_DUPFD_CLOEXEC)", errno)
			return
		}
		sink.fd = int(fd)
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0)
		if errno != 0 {
			_ = syscall.Close(sink.fd)
			sink.fd = -1
			dupErr = os.NewSyscallError("fcntl(F_GETFL)", errno)
			return
		}
		sink.flags = int(flags)
	})
	if err != nil {
		return sink, err
	}
	if dupErr != nil {
		return sink, dupErr
	}
	return sink, nil
}

func (s *captureSink) prepare() error {
	if s.flags&syscall.O_NONBLOCK == 0 {
		if err := syscall.SetNonblock(s.fd, true); err != nil {
			return os.NewSyscallError("fcntl(O_NONBLOCK)", err)
		}
		s.changed = true
	}
	// NewFile recognizes the nonblocking fd and registers pipes/sockets with
	// Go's poller, allowing Close/deadlines to cancel a blocked tee write.
	// dup shares status flags with the original descriptor. During capture the
	// original is therefore nonblocking too; closeSinks restores its flags.
	s.file = os.NewFile(uintptr(s.fd), "ghok-capture-tee")
	return nil
}

func (c *capture) closeWrites() {
	c.writesOnce.Do(func() {
		if c.stdout != nil {
			_ = c.stdout.Close()
		}
		if c.stderr != nil {
			_ = c.stderr.Close()
		}
	})
}

func (s *captureStream) pump(limit int64) {
	defer close(s.done)
	defer func() { _ = s.read.Close() }()
	buf := make([]byte, 32*1024)
	for {
		n, err := s.read.Read(buf)
		if n > 0 {
			s.result.Size += int64(n)
			remaining := limit - int64(len(s.result.Data))
			if remaining > int64(n) {
				remaining = int64(n)
			}
			if remaining > 0 {
				s.result.Data = append(s.result.Data, buf[:int(remaining)]...)
			}
			s.result.Truncated = s.result.Size > limit
			// Write immediately, including bytes beyond the capture prefix. A
			// failed tee closes the read end instead of silently draining; the
			// child's next write then sees SIGPIPE/EPIPE.
			if _, writeErr := s.sink.file.Write(buf[:n]); writeErr != nil {
				s.result.Error = writeErr
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				s.result.Error = err
			}
			return
		}
	}
}

func (c *capture) finish() (*Captured, *Captured) {
	c.closeWrites()
	// EOF, not the direct child's lifetime, defines completion: a descendant
	// may still own a write end. Context cancellation calls close separately.
	c.join()
	c.cleanupOnce.Do(c.closeSinks)
	return &c.out.result, &c.err.result
}

// close aborts a failed spawn (or any abandoned capture) and joins both pumps.
// It can run concurrently with finish, including when a downstream pipe is full.
func (c *capture) close() {
	c.abortOnce.Do(func() {
		c.closeWrites()
		for _, s := range []*captureStream{c.out, c.err} {
			// Expiring the poller's deadlines unblocks both sides without
			// closing the sink before its shared status flags can be restored.
			// Regular files/devices without deadline support cannot have a
			// stalled kernel write interrupted portably.
			_ = s.read.SetReadDeadline(time.Now())
			_ = s.sink.file.SetWriteDeadline(time.Now())
		}
	})
	c.join()
	c.cleanupOnce.Do(c.closeSinks)
}

func (c *capture) join() {
	<-c.out.done
	<-c.err.done
}

func (c *capture) closeSinks() {
	// Restore only after BOTH writers are finished, including when the two
	// descriptors share the same open-file description.
	for _, s := range []*captureStream{c.out, c.err} {
		if s.sink.fd >= 0 && s.sink.changed {
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(s.sink.fd), syscall.F_SETFL, uintptr(s.sink.flags))
			if errno != 0 && s.result.Error == nil {
				s.result.Error = os.NewSyscallError("fcntl(F_SETFL)", errno)
			}
		}
	}
	for _, s := range []*captureStream{c.out, c.err} {
		if s.sink.file != nil {
			_ = s.sink.file.Close()
		} else if s.sink.fd >= 0 {
			_ = syscall.Close(s.sink.fd)
		}
		s.sink.fd = -1
	}
}
