//go:build unix

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
	"go.yaml.in/yaml/v3"
)

func TestNoConfigExecPreservesStreamsWithoutLogs(t *testing.T) {
	for _, repository := range []bool{false, true} {
		t.Run(fmt.Sprintf("repository=%v", repository), func(t *testing.T) {
			s := newSandbox(t)
			if err := os.RemoveAll(filepath.Dir(s.cfg)); err != nil {
				t.Fatal(err)
			}
			if repository {
				if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			input := []byte("body\x00\xff\n")
			out := []byte("output\x00\n")
			setBytes(s.env, "FAKEGH_STDOUT_B64", out)
			s.env["FAKEGH_EXIT"] = "42"
			args := []string{"pr", "create", "--body-file", "-", "--", "unchanged argument"}
			result := s.gh(input, args...)
			result.code(t, 42)
			if !bytes.Equal(result.stdout, out) || len(result.stderr) != 0 {
				t.Fatalf("streams changed: stdout=%q stderr=%q", result.stdout, result.stderr)
			}
			s.onlyRecord(args, input)
			if _, err := os.Stat(filepath.Dir(s.cfg)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("no-config invocation created config/log/state files: %v", err)
			}
		})
	}
}

func TestAnyConfigEnablesDefaultLogging(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprintf("project=%v", project), func(t *testing.T) {
			s := newSandbox(t)
			if project {
				if err := os.Remove(s.cfg); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(s.cwd, ".ghok.yaml"), []byte("version: 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(s.cfg, []byte("version: 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			s.gh(nil, "version").code(t, 0)
			logs := s.logs()
			if len(logs) != 1 || logs[0].Decision != "allow" {
				t.Fatalf("configured invocation was not logged: %+v", logs)
			}
		})
	}
}

func warningProject(t *testing.T, s *sandbox, change func(*config.Config)) (string, []byte) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	no := false
	disabled := s.hook("disabled", "pre")
	disabled.Enabled = &no
	c := &config.Config{Version: 1, Hooks: []config.Hook{s.hook("first", "pre"), s.hook("second", "pre"), disabled}}
	for i := range c.Hooks {
		c.Hooks[i].OnError = config.OnErrorAllow
	}
	if change != nil {
		change(c)
	}
	c.ApplyDefaults()
	data, err := yaml.Marshal(struct {
		Version int             `yaml:"version"`
		Hooks   config.HookList `yaml:"hooks,omitempty"`
		Trust   config.Trust    `yaml:"trust,omitempty"`
	}{Version: c.Version, Hooks: c.Hooks, Trust: c.Trust})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.cwd, ".ghok.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestIgnoredHooksWarningThrottleAndManifestChanges(t *testing.T) {
	s := newSandbox(t)
	project, data := warningProject(t, s, func(c *config.Config) {
		c.Trust.Include = []string{"dep0", "dep1", "dep2", "dep3"}
	})
	invoke := func(want bool, reason string) []byte {
		t.Helper()
		result := s.gh(nil, "version")
		result.code(t, 0)
		if !want {
			if len(result.stderr) != 0 {
				t.Fatalf("unexpected warning: %s", result.stderr)
			}
			return result.stderr
		}
		if bytes.Count(result.stderr, []byte("\n")) != 1 || !bytes.Contains(result.stderr, []byte("2 project hooks")) || !bytes.Contains(result.stderr, []byte(reason)) || !bytes.Contains(result.stderr, []byte("ghok config trust")) {
			t.Fatalf("missing one-line actionable hook warning: %s", result.stderr)
		}
		return result.stderr
	}
	invoke(true, "untrusted")
	invoke(false, "")
	warnings, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg), "state", "ignored-hooks-*.warning"))
	if err != nil || len(warnings) != 1 {
		t.Fatalf("warning state=%v: %v", warnings, err)
	}
	if err := os.WriteFile(warnings[0], []byte(time.Now().Add(-11*time.Minute).UTC().Format(time.RFC3339Nano)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(true, "untrusted")
	s.management(nil, "config", "trust").code(t, 0)
	invoke(false, "")
	for i := range 4 {
		if err := os.WriteFile(filepath.Join(s.cwd, fmt.Sprintf("dep%d", i)), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	warning := invoke(true, "changed since trust")
	if !bytes.Contains(warning, []byte("dep0, dep1, dep2, …and 1 more")) || bytes.Contains(warning, []byte("dep3")) {
		t.Fatalf("changed-file warning is not bounded: %s", warning)
	}
	invoke(false, "")
	if err := os.WriteFile(project, append(data, []byte("\n# new manifest\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(false, "")
}

func TestIgnoredHooksWarningSuppression(t *testing.T) {
	for _, scenario := range []string{"disabled-warning", "disabled-hooks", "policy-only", "nested", "invalid-user", "invalid-project", "fail-closed"} {
		t.Run(scenario, func(t *testing.T) {
			s := newSandbox(t)
			project, _ := warningProject(t, s, func(c *config.Config) {
				switch scenario {
				case "disabled-hooks":
					no := false
					for i := range c.Hooks {
						c.Hooks[i].Enabled = &no
					}
				case "policy-only":
					c.Hooks = nil
				}
			})
			wantCode := 0
			switch scenario {
			case "disabled-warning":
				s.configure(func(c *config.Config) { no := false; c.Logging.IgnoredHooksWarning = &no })
			case "nested":
				token, cleanup, err := install.NewNestedToken(filepath.Dir(s.cfg))
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				s.env["GHOK_NESTED"], s.env["GHOK_NESTED_TOKEN"] = "1", token
			case "invalid-user", "fail-closed":
				if err := os.WriteFile(s.cfg, []byte("version: 1\nunknown: true\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if scenario == "fail-closed" {
					s.env["GHOK_FAIL_CLOSED"] = "1"
					wantCode = 78
				}
			case "invalid-project":
				if err := os.WriteFile(project, []byte("logging: {}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result := s.gh(nil, "version")
			result.code(t, wantCode)
			if strings.Contains(string(result.stderr), "project hooks") {
				t.Fatalf("ignored-hook warning was not suppressed: %s", result.stderr)
			}
			warnings, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg), "state", "ignored-hooks-*.warning"))
			if err != nil || len(warnings) != 0 {
				t.Fatalf("suppressed warning consumed throttle: %v %v", warnings, err)
			}
		})
	}
}
