//go:build linux

package process

import (
	"bytes"
	"os"
	"strconv"
)

func childStillStopped(pid int) bool {
	// Wait4 may report a stop that predates the shell's SIGCONT. The stat
	// state follows the parenthesized comm (which itself can contain ')').
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		return true
	}
	return b[i+2] == 'T' || b[i+2] == 't'
}
