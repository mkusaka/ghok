//go:build unix

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/install"
)

func TestExecWithoutShimPreservesInvocation(t *testing.T) {
	for _, path := range []string{"no-config", "fast", "checked", "supervised"} {
		for _, code := range []int{0, 1, 2, 4, 8, 42} {
			t.Run(fmt.Sprintf("%s/exit=%d", path, code), func(t *testing.T) {
				s := newSandbox(t)
				s.env["PATH"] = filepath.Dir(s.real) // No shim on PATH.
				s.env["FAKEGH_EXIT"] = strconv.Itoa(code)
				s.configure(func(c *config.Config) {
					if path == "checked" || path == "supervised" {
						yes := true
						c.Logging.Enabled = &yes
						c.Hooks = []config.Hook{s.hook("pre", "pre")}
						if path == "supervised" {
							c.Hooks = append(c.Hooks, s.hook("post", "post"))
						}
					}
				})
				if path == "no-config" {
					if err := os.Remove(s.cfg); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"version", "--phase", "post", "--exit-code=78", "--", "--show-secrets", "", "space argument"}
				input := []byte{0, 'x', '\n', 255}
				stdout, stderr := []byte{255, 'o', '\n'}, []byte{0, 'e', '\n'}
				setBytes(s.env, "FAKEGH_STDOUT_B64", stdout)
				setBytes(s.env, "FAKEGH_STDERR_B64", stderr)
				result := s.management(input, append([]string{"exec", "--"}, args...)...)
				result.code(t, code)
				if !bytes.Equal(result.stdout, stdout) || !bytes.Equal(result.stderr, stderr) {
					t.Fatalf("streams changed: stdout=%q stderr=%q", result.stdout, result.stderr)
				}
				record := s.onlyRecord(args, input)
				wantEnv := make(map[string]string, len(s.env)+1)
				for k, v := range s.env {
					wantEnv[k] = v
				}
				wantEnv["GHOK_DEPTH"] = "1"
				gotEnv := envMap(record.Env)
				if record.Argv0 != "gh" || len(gotEnv) != len(wantEnv) {
					t.Fatalf("argv0=%q env=%v, want gh / %v", record.Argv0, gotEnv, wantEnv)
				}
				for k, v := range wantEnv {
					if gotEnv[k] != v {
						t.Fatalf("env[%s]=%q, want %q", k, gotEnv[k], v)
					}
				}
				if path == "checked" || path == "supervised" {
					if in := s.hookInput("pre"); in.Invocation.Via != "exec" {
						t.Fatalf("pre via=%q", in.Invocation.Via)
					}
					if path == "supervised" {
						in := s.hookInput("post")
						if in.Invocation.Via != "exec" || in.Result == nil || in.Result.ExitCode != code {
							t.Fatalf("post input=%+v", in)
						}
					}
					if logs := s.logs(); len(logs) != 1 || logs[0].Invocation.Via != "exec" {
						t.Fatalf("exec provenance logs=%+v", logs)
					}
				}
			})
		}
	}
}

func TestExecPreservesSignals(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
			t.Run(fmt.Sprintf("supervised=%t/signal=%d", supervised, sig), func(t *testing.T) {
				s := newSandbox(t)
				s.env["PATH"] = filepath.Dir(s.real)
				s.env["FAKEGH_SIGNAL"] = strconv.Itoa(int(sig))
				if supervised {
					s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("post", "post")} })
				}
				s.management(nil, "exec", "--", "version").signaled(t, sig)
				if record := s.onlyRecord([]string{"version"}, nil); record.Argv0 != "gh" {
					t.Fatalf("argv0=%q", record.Argv0)
				}
				if supervised {
					in := s.hookInput("post")
					want := "SIGTERM"
					if sig == syscall.SIGINT {
						want = "SIGINT"
					}
					if in.Result == nil || in.Result.ExitCode != -1 || in.Result.Signal != want {
						t.Fatalf("post result=%+v", in.Result)
					}
				}
			})
		}
	}
}

func TestExecRecursionAndNestedChecks(t *testing.T) {
	for _, depth := range []string{"3", "4", "invalid"} {
		t.Run("depth="+depth, func(t *testing.T) {
			s := newSandbox(t)
			s.env["PATH"], s.env["GHOK_DEPTH"] = filepath.Dir(s.real), depth
			if err := os.Remove(s.cfg); err != nil {
				t.Fatal(err)
			}
			result := s.management(nil, "exec", "--", "version")
			if depth == "3" {
				result.code(t, 0)
				if child := envMap(s.onlyRecord([]string{"version"}, nil).Env); child["GHOK_DEPTH"] != "4" {
					t.Fatalf("depth=%q", child["GHOK_DEPTH"])
				}
			} else {
				result.code(t, 1)
				s.absentRecord()
				if !bytes.Contains(result.stderr, []byte("GHOK_DEPTH")) {
					t.Fatalf("depth diagnostic=%q", result.stderr)
				}
			}
		})
	}
	for _, token := range []string{"none", "forged", "valid"} {
		t.Run("token="+token, func(t *testing.T) {
			s := newSandbox(t)
			s.env["PATH"], s.env["GHOK_NESTED"] = filepath.Dir(s.real), "1"
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				c.Policy.Mode = config.ModeEnforce
				c.Policy.Rules = []config.Rule{{ID: "blocked", Decision: "deny"}}
			})
			switch token {
			case "forged":
				s.env["GHOK_NESTED_TOKEN"] = strings.Repeat("a", 64)
			case "valid":
				value, cleanup, err := install.NewNestedToken(filepath.Dir(s.cfg))
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				s.env["GHOK_NESTED_TOKEN"] = value
			}
			result := s.management(nil, "exec", "--", "version")
			if token == "valid" {
				result.code(t, 0)
				s.onlyRecord([]string{"version"}, nil)
				if logs := s.logs(); len(logs) != 1 || logs[0].Decision != "passthrough-nested" || logs[0].Invocation.Via != "exec" {
					t.Fatalf("nested logs=%+v", logs)
				}
			} else {
				result.code(t, 77)
				s.absentRecord()
				if !bytes.HasPrefix(result.stderr, []byte("ghok: denied by rule blocked\n")) {
					t.Fatalf("nested marker bypassed policy: %q", result.stderr)
				}
			}
		})
	}
}

func TestExecRejectsShimOverridesAndMissingGh(t *testing.T) {
	for _, kind := range []string{"environment", "config", "self-on-path", "missing"} {
		t.Run(kind, func(t *testing.T) {
			s := newSandbox(t)
			s.env["PATH"] = s.bin
			if kind == "environment" {
				s.env["GHOK_REAL_GH"] = filepath.Join(s.bin, "gh")
			}
			if kind == "config" {
				s.configure(func(c *config.Config) { c.Runtime.RealGh = filepath.Join(s.bin, "gh") })
			}
			if kind == "missing" {
				s.env["PATH"] = filepath.Join(s.root, "missing")
			}
			result := s.management(nil, "exec", "--", "version")
			result.code(t, 127)
			s.absentRecord()
			if kind != "missing" && !bytes.Contains(result.stderr, []byte("ghok shim")) && !bytes.Contains(result.stderr, []byte("skipped:")) {
				t.Fatalf("discovery diagnostic=%q", result.stderr)
			}
		})
	}
}

func TestShimInvocationProvenance(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
		c.Hooks = []config.Hook{s.hook("pre", "pre")}
	})
	s.gh(nil, "version").code(t, 0)
	if in := s.hookInput("pre"); in.Invocation.Via != "shim" {
		t.Fatalf("shim via=%q", in.Invocation.Via)
	}
	if logs := s.logs(); len(logs) != 1 || logs[0].Invocation.Via != "shim" {
		t.Fatalf("shim provenance logs=%+v", logs)
	}
}
