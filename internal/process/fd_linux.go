//go:build linux

package process

import "syscall"

func dupStandardInput(fd int) error {
	if fd == 0 {
		return nil
	}
	// Linux arm64 has dup3 but no dup2 syscall. Flags=0 clears FD_CLOEXEC.
	return syscall.Dup3(fd, 0, 0)
}
