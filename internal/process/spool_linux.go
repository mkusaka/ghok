//go:build linux

package process

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const spoolProbeFIFO = false

func spoolPoll(fds *[2]spoolPollFD, timeout time.Duration) error {
	var ts syscall.Timespec
	var timeoutPtr *syscall.Timespec
	if timeout >= 0 {
		ts = syscall.NsecToTimespec(int64(timeout))
		timeoutPtr = &ts
	}
	// ppoll is available on both Linux amd64 and arm64 (poll is not on arm64).
	_, _, errno := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(fds)), 2, uintptr(unsafe.Pointer(timeoutPtr)), 0, 0, 0)
	runtime.KeepAlive(fds)
	runtime.KeepAlive(&ts)
	if errno != 0 {
		return errno
	}
	return nil
}
