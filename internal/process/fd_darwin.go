//go:build darwin

package process

import "syscall"

func dupStandardInput(fd int) error { return syscall.Dup2(fd, 0) }
