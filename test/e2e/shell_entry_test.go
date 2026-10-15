//go:build unix

package e2e

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
)

func TestShellInit(t *testing.T) {
	s := newSandbox(t)
	for shell, fn := range map[string]string{"zsh": `function gh { ghok exec -- "$@"; }`, "bash": `function gh { ghok exec -- "$@"; }`, "fish": "function gh; ghok exec -- $argv; end"} {
		result := s.management(nil, "shell-init", shell)
		result.code(t, 0)
		if !bytes.HasPrefix(result.stdout, []byte("# ghok:")) || !bytes.Contains(result.stdout, []byte("ghok shim install")) || !bytes.HasSuffix(result.stdout, []byte(fn+"\n")) {
			t.Fatalf("%s: %s", shell, result.stdout)
		}
	}
	s.management(nil, "shell-init", "tcsh").code(t, 2)
	s.management(nil, "shell-init").code(t, 2)
}

// The function and the shim may coexist: ghok exec skips the shim during
// discovery, so real gh runs exactly once and policy runs once.
func TestShellFunctionWithShim(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " not installed")
			}
			s := newSandbox(t)
			s.env["PATH"] = strings.Join([]string{filepath.Dir(ghokBinary), s.bin, filepath.Dir(s.real)}, string(filepath.ListSeparator))
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
			})
			s.run(path, nil, "-c", `eval "$(ghok shell-init `+shell+`)"; gh version`).code(t, 0)
			if records := s.records(); len(records) != 1 || records[0].Argv0 != "gh" {
				t.Fatalf("real gh records=%+v", records)
			}
			if logs := s.logs(); len(logs) != 1 || logs[0].Invocation.Via != "exec" {
				t.Fatalf("logs=%+v", logs)
			}
		})
	}
}

func TestDoctorShellEntry(t *testing.T) {
	s := newSandbox(t)
	s.management(nil, "shim", "uninstall", "--bin-dir", s.bin).code(t, 0)
	s.env["PATH"] = filepath.Dir(s.real)
	setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
	doctor := func() map[string]install.Check {
		t.Helper()
		result := s.management(nil, "doctor", "--json")
		result.code(t, 0)
		var checks []install.Check
		decodeJSON(t, result.stdout, &checks)
		byName := make(map[string]install.Check)
		for _, c := range checks {
			byName[c.Name] = c
		}
		return byName
	}
	checks := doctor()
	if _, ok := checks["shell-entry"]; ok || checks["shim-ownership"].Status != install.StatusWarn {
		t.Fatalf("before exec: %+v", checks)
	}
	s.management(nil, "exec", "--", "version").code(t, 0)
	checks = doctor()
	entry := checks["shell-entry"]
	if entry.Status != install.StatusInfo || !strings.Contains(entry.Detail, "bypasses ghok") || entry.Fix == "" || !regexp.MustCompile(`last used \d{4}-\d\d-\d\d \d\d:\d\d:\d\d [+-]\d\d:\d\d `).MatchString(entry.Detail) {
		t.Fatalf("shell entry: %+v", entry)
	}
	for _, name := range []string{"path-precedence", "shim-ownership"} {
		if checks[name].Status != install.StatusInfo {
			t.Fatalf("%s not downgraded: %+v", name, checks[name])
		}
	}
	s.management(nil, "shim", "install", "--bin-dir", s.bin).code(t, 0)
	s.env["PATH"] = s.bin + string(filepath.ListSeparator) + filepath.Dir(s.real)
	checks = doctor()
	if entry := checks["shell-entry"]; !strings.Contains(entry.Detail, "shim also covers") || checks["shim-ownership"].Status != install.StatusOK {
		t.Fatalf("with shim: %+v", checks)
	}
}
