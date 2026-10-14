//go:build darwin

package process

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const spoolProbeFIFO = true

func spoolPoll(fds *[2]spoolPollFD, timeout time.Duration) error {
	millis := int64(-1)
	if timeout >= 0 {
		millis = int64(timeout / time.Millisecond)
		if timeout%time.Millisecond != 0 {
			millis++
		}
		if millis > 1<<31-1 {
			millis = 1<<31 - 1
		}
	}
	// kqueue has FIFO EOF limitations on Darwin; poll handles inherited stdin
	// without registering it with the Go runtime or changing O_NONBLOCK.
	_, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(fds)), 2, uintptr(millis))
	runtime.KeepAlive(fds)
	if errno != 0 {
		return errno
	}
	return nil
}
