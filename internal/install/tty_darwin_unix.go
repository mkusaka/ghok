//go:build darwin && unix

package install

import (
	"os"
	"syscall"
	"unsafe"
)

func stdinTTY(f *os.File) bool {
	var term syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&term)))
	return err == 0
}
