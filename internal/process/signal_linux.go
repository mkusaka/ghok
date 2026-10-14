//go:build linux

package process

import (
	"syscall"
	"unsafe"
)

// Linux's kernel sigaction layout for amd64/arm64 (kernel sigsets are 8 bytes).
type kernelSigaction struct {
	handler  uintptr
	flags    uintptr
	restorer uintptr
	mask     uint64
}

func defaultSignal(sig syscall.Signal) error {
	// SIGKILL is uncatchable and unblockable; rt_sigaction would reject it.
	if sig == syscall.SIGKILL {
		return nil
	}
	var action kernelSigaction
	_, _, err := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&action)), 0, 8, 0, 0)
	if err != 0 {
		return err
	}
	mask := uint64(1) << (uint(sig) - 1)
	_, _, err = syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 1 /* SIG_UNBLOCK */, uintptr(unsafe.Pointer(&mask)), 0, 8, 0, 0)
	if err != 0 {
		return err
	}
	return nil
}

func raiseSignal(sig syscall.Signal) {
	_, _, _ = syscall.RawSyscall(syscall.SYS_TGKILL, uintptr(syscall.Getpid()), uintptr(syscall.Gettid()), uintptr(sig))
}
