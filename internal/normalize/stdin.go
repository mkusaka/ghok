package normalize

import (
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
	"github.com/mkusaka/ghok/internal/normalize/native"
)

// NeedsStdin reports whether alias-expanded arguments request stdin for
// normalization. Which is "api", "body", or "notes" when needed, otherwise "".
// Invalid arguments and help requests do not require input.
func NeedsStdin(expandedArgv []string) (needed bool, which string) {
	if len(expandedArgv) > 0 && expandedArgv[0] == "api" {
		if apiargs.NeedsStdinArgv(expandedArgv) {
			return true, "api"
		}
		return false, ""
	}
	return native.NeedsStdin(expandedArgv)
}
