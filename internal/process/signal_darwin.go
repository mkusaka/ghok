//go:build darwin

package process

import (
	"syscall"
	"unsafe"
)

// Darwin must use libSystem rather than raw sigaction syscalls. This is the
// same CGO-free dynamic-call mechanism used by Go's syscall package.
//
//go:linkname darwinLibcCall syscall.syscall
//go:noescape
func darwinLibcCall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

func sigactionAddress() uintptr
func sigprocmaskAddress() uintptr
func raiseAddress() uintptr
func sigactionTrampoline()
func sigprocmaskTrampoline()
func raiseTrampoline()

//go:cgo_import_dynamic process_sigaction sigaction "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic process_sigprocmask sigprocmask "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic process_raise raise "/usr/lib/libSystem.B.dylib"

type darwinSigaction struct {
	handler uintptr
	mask    uint32
	flags   int32
}

func defaultSignal(sig syscall.Signal) error {
	var action darwinSigaction
	r, _, err := darwinLibcCall(sigactionAddress(), uintptr(sig), uintptr(unsafe.Pointer(&action)), 0)
	if int64(r) == -1 {
		return err
	}
	mask := uint32(1) << (uint(sig) - 1)
	r, _, err = darwinLibcCall(sigprocmaskAddress(), 2 /* SIG_UNBLOCK */, uintptr(unsafe.Pointer(&mask)), 0)
	if int64(r) == -1 {
		return err
	}
	return nil
}

func raiseSignal(sig syscall.Signal) {
	_, _, _ = darwinLibcCall(raiseAddress(), uintptr(sig), 0, 0)
}
