package install

import (
	"fmt"
	"strconv"
	"strings"
)

const DepthEnv = "GHOK_DEPTH"
const MaxDepth = 4

// NextDepth returns a fresh environment with exactly one incremented depth.
// Malformed, negative, or duplicate values are errors, not a reset of the guard.
func NextDepth(env []string) (newEnv []string, depth int, err error) {
	found := false
	for _, entry := range env {
		value, ok := strings.CutPrefix(entry, DepthEnv+"=")
		if !ok {
			continue
		}
		if found {
			return nil, 0, fmt.Errorf("duplicate %s environment variable", DepthEnv)
		}
		found = true
		depth, err = strconv.Atoi(value)
		if err != nil || depth < 0 {
			return nil, 0, fmt.Errorf("invalid %s value %q: expected a nonnegative integer", DepthEnv, value)
		}
	}
	if depth >= MaxDepth {
		return nil, MaxDepth + 1, fmt.Errorf("%s recursion limit %d exceeded; check real gh discovery", DepthEnv, MaxDepth)
	}
	depth++
	newEnv = make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, DepthEnv+"=") {
			newEnv = append(newEnv, entry)
		}
	}
	newEnv = append(newEnv, DepthEnv+"="+strconv.Itoa(depth))
	return newEnv, depth, nil
}
