package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type EnableOptions struct{ BinDir, Self, PathEnv, StateDir string }
type EnableResult struct {
	ShimPath   string
	Created    bool
	RealGh     string
	OnPath     bool
	Precedence string
	FixHint    string
}
type DisableOptions struct{ BinDir, Self, StateDir string }

// shimState maps absolute shim paths to their absolute, recorded link targets.
type shimState map[string]string

func loadState(dir string) (shimState, error) {
	s := make(shimState)
	if dir == "" {
		return s, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "shims.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read shim state: %w", err)
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode shim state: %w", err)
	}
	if s == nil {
		return nil, errors.New("invalid null shim state")
	}
	return s, nil
}

func saveState(dir string, s shimState) error {
	if dir == "" {
		return errors.New("StateDir is required for shim management")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".shims-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, "shims.json"))
}

func shimPath(binDir string) (string, error) {
	if binDir == "" {
		return "", errors.New("BinDir is required")
	}
	path, err := filepath.Abs(filepath.Join(binDir, "gh"))
	if err != nil {
		return "", err
	}
	// Canonicalize the parent so state survives alternate spellings of BinDir.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(parent, "gh")
	}
	return path, nil
}

func ownedShim(path, self string, s shimState) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	target = filepath.Clean(target)
	// A stale path-only record must not authorize deleting a replacement link.
	if recorded, ok := s[path]; ok && recorded == target {
		return true, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, nil
	}
	actual, err := os.Stat(resolved)
	if err != nil {
		return false, err
	}
	if !actual.Mode().IsRegular() {
		return false, nil
	}
	if filepath.Base(resolved) == "ghok" {
		return true, nil
	}
	current, err := os.Stat(self)
	if err != nil {
		return false, nil
	}
	return os.SameFile(actual, current), nil
}

// IsOwnedShim recognizes symlinks by matching recorded target, resolved ghok
// basename, or SameFile with Self. Regular files are never owned shims.
func IsOwnedShim(path, self, stateDir string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	s, err := loadState(stateDir)
	if err != nil {
		return false, err
	}
	return ownedShim(abs, self, s)
}

// Enable creates or atomically updates an owned link; never changes shell rc files.
// ProbeShim is separate: the caller probes after installing the wrapper handler.
func Enable(o EnableOptions) (EnableResult, error) {
	var result EnableResult
	if o.StateDir == "" {
		return result, errors.New("StateDir is required for shim management")
	}
	self, err := filepath.Abs(o.Self)
	if err != nil {
		return result, err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return result, fmt.Errorf("resolve ghok executable: %w", err)
	}
	info, err := os.Stat(self)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return result, errors.New("ghok executable must be a regular executable file")
	}
	if o.BinDir == "" {
		return result, errors.New("BinDir is required")
	}
	if err = os.MkdirAll(o.BinDir, 0755); err != nil {
		return result, err
	}
	path, err := shimPath(o.BinDir)
	if err != nil {
		return result, err
	}
	result.ShimPath = path
	s, err := loadState(o.StateDir)
	if err != nil {
		return result, err
	}
	_, existsErr := os.Lstat(path)
	if existsErr != nil && !errors.Is(existsErr, os.ErrNotExist) {
		return result, existsErr
	}
	if existsErr == nil {
		owned, err := ownedShim(path, self, s)
		if err != nil {
			return result, err
		}
		if !owned {
			detail := "file"
			if target, err := os.Readlink(path); err == nil {
				detail = "symlink to " + target
			}
			return result, fmt.Errorf("%s exists and is not managed by ghok (%s); remove it or choose --bin-dir", path, detail)
		}
	}
	_, candidates, err := FindRealGh(DiscoverOptions{PathEnv: o.PathEnv, Self: self})
	if err != nil {
		return result, err
	}
	oldSelf, _ := os.Stat(path)
	for _, candidate := range candidates {
		if candidate.Skipped != "" {
			continue
		}
		owned, err := ownedShim(candidate.Path, self, s)
		if err != nil {
			return result, err
		}
		info, err := os.Stat(candidate.Resolved)
		if err != nil || owned || oldSelf != nil && os.SameFile(oldSelf, info) {
			continue
		}
		result.RealGh = candidate.Path
		break
	}
	if result.RealGh == "" {
		return result, errors.New("no real gh executable found after excluding owned shims")
	}
	if existsErr == nil {
		target, err := os.Readlink(path)
		if err != nil {
			return result, err
		}
		if target != self {
			if err = replaceShim(path, self, s); err != nil {
				return result, err
			}
		}
	} else {
		// Symlink creation fails on collision instead of replacing a concurrent file.
		if err = os.Symlink(self, path); err != nil {
			return result, err
		}
		result.Created = true
	}
	s[path] = self
	if err = saveState(o.StateDir, s); err != nil {
		if result.Created {
			// Roll back only if our exact newly-created link is still present.
			if target, e := os.Readlink(path); e == nil && target == self {
				_ = os.Remove(path)
			}
		}
		return result, fmt.Errorf("save shim ownership: %w", err)
	}
	result.OnPath, result.Precedence, result.FixHint = pathPrecedence(path, o.PathEnv)
	return result, nil
}

func replaceShim(path, self string, s shimState) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ghok-shim-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	if err = f.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err = os.Remove(temp); err != nil {
		return err
	}
	defer os.Remove(temp)
	if err = os.Symlink(self, temp); err != nil {
		return err
	}
	owned, err := ownedShim(path, self, s)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("%s is no longer an owned shim", path)
	}
	return os.Rename(temp, path)
}

// Disable only inspects the requested BinDir. It does not remove other installs.
func Disable(o DisableOptions) ([]string, error) {
	if o.StateDir == "" {
		return nil, errors.New("StateDir is required for shim management")
	}
	path, err := shimPath(o.BinDir)
	if err != nil {
		return nil, err
	}
	s, err := loadState(o.StateDir)
	if err != nil {
		return nil, err
	}
	owned, err := ownedShim(path, o.Self, s)
	if err != nil {
		return nil, err
	}
	var removed []string
	if owned {
		if err = os.Remove(path); err != nil {
			return nil, err
		}
		removed = append(removed, path)
	}
	if _, recorded := s[path]; recorded {
		delete(s, path)
		if err = saveState(o.StateDir, s); err != nil {
			return removed, fmt.Errorf("save shim ownership: %w", err)
		}
	}
	return removed, nil
}

func pathPrecedence(shim, pathEnv string) (bool, string, string) {
	var first string
	onPath := false
	for _, dir := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		p, err := filepath.Abs(filepath.Join(dir, "gh"))
		if err != nil {
			continue
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			p = filepath.Join(parent, "gh")
		}
		if p == shim {
			onPath = true
		}
		if first == "" {
			if c := inspectCandidate(p, nil); c.Resolved != "" {
				info, err := os.Stat(c.Resolved)
				if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
					first = p
				}
			}
		}
	}
	if first == shim {
		return onPath, shim + " is the first executable gh on PATH", ""
	}
	detail := filepath.Dir(shim) + " is not on PATH"
	if onPath {
		detail = first + " precedes " + shim + " on PATH"
	}
	// Quote arbitrary custom directories safely for a POSIX shell.
	bin := "'" + strings.ReplaceAll(filepath.Dir(shim), "'", "'\"'\"'") + "'"
	if home, err := os.UserHomeDir(); err == nil && filepath.Dir(shim) == filepath.Join(home, ".local", "bin") {
		bin = "\"$HOME/.local/bin\""
	}
	return onPath, detail, "export PATH=" + bin + ":\"$PATH\""
}

// ProbeShim verifies the wrapper's probe protocol without invoking normal gh.
func ProbeShim(shim string, env []string) error {
	probeEnv := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "GHOK_PROBE=") {
			probeEnv = append(probeEnv, e)
		}
	}
	probeEnv = append(probeEnv, "GHOK_PROBE=1")
	out, err := runDiagnostic(shim, probeEnv)
	if err != nil {
		return fmt.Errorf("shim probe failed: %w", err)
	}
	if out != "ghok-probe\n" {
		return fmt.Errorf("shim probe returned %s, expected ghok-probe newline", strconv.Quote(out))
	}
	return nil
}
