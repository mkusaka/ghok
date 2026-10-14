//go:build unix

package install

import "os"

// IsTTY reports whether f is a terminal without consuming input.
func IsTTY(f *os.File) bool { return f != nil && stdinTTY(f) }
