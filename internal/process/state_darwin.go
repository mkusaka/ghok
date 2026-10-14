//go:build darwin

package process

import (
	"syscall"
	"unsafe"
)

//go:linkname darwinLibcCall6 syscall.syscall6
//go:noescape
func darwinLibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:cgo_import_dynamic process_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"
func pidinfoAddress() uintptr

//nolint:unused // referenced from signal_darwin_arm64.s and signal_darwin_amd64.s.
func pidinfoTrampoline()

func childStillStopped(pid int) bool {
	// proc_bsdshortinfo: pid, ppid, pgid, status, 16-byte comm, eight u32s.
	// sys/proc_info.h PROC_PIDT_SHORTBSDINFO=13; sys/proc.h SSTOP=4.
	var info [16]uint32
	n, _, _ := darwinLibcCall6(pidinfoAddress(), uintptr(pid), 13, 0, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
	// Failure must not suppress a genuine stop event.
	return n != unsafe.Sizeof(info) || info[3] == 4
}
