//go:build unix

package e2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/delegates"
)

func delegateFixture(s *sandbox) config.Delegate {
	return config.Delegate{ID: "relay", Match: config.Match{Effects: config.StringList{"read"}, Levels: config.StringList{"L1"}}, Command: []string{fakeBinary}, Env: map[string]string{"FAKEGH_RECORD": filepath.Join(s.root, "delegate.jsonl")}}
}

func (s *sandbox) delegateRecords() []invocationRecord {
	s.t.Helper()
	original := s.record
	s.record = filepath.Join(s.root, "delegate.jsonl")
	defer func() { s.record = original }()
	return s.records()
}

func TestDelegatePassthroughAndPostStatus(t *testing.T) {
	for _, exit := range []int{0, 1, 2, 4, 8, 42} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			s := newSandbox(t)
			body := []byte{0, 'a', '\n', 255, 0}
			out, errout := []byte{0, 'o', 255, '\n'}, []byte{'e', 0, 254, '\n'}
			s.env["CUSTOM_INHERITED"] = "spaces=and\nnewlines π"
			s.configure(func(c *config.Config) {
				d := delegateFixture(s)
				d.Command = append(d.Command, "fixed argument", "")
				d.Env["FAKEGH_EXIT"] = strconv.Itoa(exit)
				setBytes(d.Env, "FAKEGH_STDOUT_B64", out)
				setBytes(d.Env, "FAKEGH_STDERR_B64", errout)
				d.Env["OVERRIDE"] = "delegate-specific"
				c.Delegates = []config.Delegate{d}
				post := s.hook("post", "post")
				post.Match.ExitCodes = []int{exit}
				post.CaptureOutput = true
				c.Hooks = []config.Hook{s.hook("pre", "pre"), post}
			})
			args := []string{"pr", "checks", "12", "-R", "acme/project", "--json", "name,state", "--jq", `.[] | .name + "\n"`}
			result := s.gh(body, args...)
			result.code(t, exit)
			if !bytes.Equal(result.stdout, out) || !bytes.Equal(result.stderr, errout) {
				t.Fatalf("output changed: stdout=%q stderr=%q", result.stdout, result.stderr)
			}
			s.absentRecord()
			records := s.delegateRecords()
			if len(records) != 1 || !reflect.DeepEqual(records[0].Argv, append([]string{"fixed argument", ""}, args...)) {
				t.Fatalf("delegate argv: %+v", records)
			}
			r := records[0]
			input, _ := base64.StdEncoding.DecodeString(r.StdinBase64)
			cwd, _ := filepath.EvalSymlinks(s.cwd)
			if !bytes.Equal(input, body) || r.Cwd != cwd {
				t.Fatalf("stdin/cwd: %+v", r)
			}
			env := envMap(r.Env)
			for k, v := range map[string]string{"CUSTOM_INHERITED": s.env["CUSTOM_INHERITED"], "OVERRIDE": "delegate-specific", "GHOK_NO_DELEGATE": "1", "GHOK_DELEGATE_ID": "relay", "GHOK_ACTION": "pr.checks", "GHOK_REPO": "acme/project", "GHOK_REAL_GH": s.real, "GHOK_DEPTH": "1"} {
				if env[k] != v {
					t.Fatalf("env[%s]=%q want=%q", k, env[k], v)
				}
			}
			post := s.hookInput("post")
			if post.Result == nil || post.Result.ExitCode != exit || post.Result.Stdout == nil || post.Result.Stdout.Base64 != base64.StdEncoding.EncodeToString(out) {
				t.Fatalf("post result: %+v", post.Result)
			}
			s.hookInput("pre")
		})
	}
}

func TestDelegateFallback(t *testing.T) {
	for _, failure := range []string{"not-handled", "missing", "permission", "timeout", "not-handled-deny", "missing-deny", "timeout-deny", "timeout-catches-deny"} {
		t.Run(failure, func(t *testing.T) {
			s := newSandbox(t)
			input := bytes.Repeat([]byte("binary\x00input\xff\n"), 20000) // larger than a pipe buffer
			s.env["FAKEGH_EXIT"] = "8"
			setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("real gh output"))
			s.configure(func(c *config.Config) {
				d := delegateFixture(s)
				d.Env["FAKEGH_STDOUT_B64"] = ""
				d.Env["FAKEGH_EXIT"] = strconv.Itoa(delegates.NotHandled)
				d.Timeout = config.Duration(2 * time.Second)
				if strings.Contains(failure, "missing") {
					d.Command = []string{"missing-relay"}
				}
				if failure == "permission" {
					path := filepath.Join(s.root, "not-executable")
					if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0600); err != nil {
						t.Fatal(err)
					}
					d.Command = []string{path}
				}
				if strings.Contains(failure, "timeout") {
					d.Env["FAKEGH_SLEEP"] = "15s"
				}
				if failure == "timeout-catches-deny" {
					d.Env["FAKEGH_CATCH_TERM_EXIT"] = "125"
				}
				if strings.HasSuffix(failure, "-deny") {
					d.OnError = "deny"
				}
				later := delegateFixture(s)
				later.ID = "must-not-run"
				c.Delegates = []config.Delegate{d, later}
				c.Hooks = []config.Hook{s.hook("post", "post")}
			})
			args := []string{"pr", "view", "12", "-R", "acme/project"}
			result := s.gh(input, args...)
			want := 8
			if failure == "missing-deny" || strings.HasPrefix(failure, "timeout") && strings.HasSuffix(failure, "-deny") {
				want = 78
				s.absentRecord()
			} else {
				s.onlyRecord(args, input)
				if string(result.stdout) != "real gh output" {
					t.Fatalf("fallback output: %q", result.stdout)
				}
			}
			result.code(t, want)
			if !strings.Contains(string(result.stderr), "ghok: warning: delegate relay:") || bytes.Count(result.stderr, []byte("\n")) != 1 {
				t.Fatalf("one-line warning missing: %s", result.stderr)
			}
			if post := s.hookInput("post"); post.Result == nil || post.Result.ExitCode != want {
				t.Fatalf("post did not receive final result: %+v", post.Result)
			}
			if len(s.delegateRecords()) > 1 {
				t.Fatal("tried a later delegate")
			}
		})
	}
}

func TestDelegateNeverFallbackAfterOutput(t *testing.T) {
	for _, scenario := range []string{"decline-stdout", "timeout-stdout", "timeout-stderr"} {
		t.Run(scenario, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				d := delegateFixture(s)
				d.Env["FAKEGH_EXIT"] = "125"
				if strings.HasPrefix(scenario, "timeout") {
					d.Timeout = config.Duration(2 * time.Second)
					d.Env["FAKEGH_SLEEP"] = "15s"
					d.Env["FAKEGH_OUTPUT_BEFORE_SLEEP"] = "1"
				}
				key := "FAKEGH_STDOUT_B64"
				if scenario == "timeout-stderr" {
					key = "FAKEGH_STDERR_B64"
				}
				setBytes(d.Env, key, []byte("partial output\n"))
				c.Delegates = []config.Delegate{d}
			})
			r := s.gh(nil, "pr", "view", "12")
			r.code(t, 78)
			s.absentRecord()
			if !strings.Contains(string(r.stderr), "not running gh") {
				t.Fatalf("unsafe retry not diagnosed: %s", r.stderr)
			}
		})
	}
}

func TestDelegateGuardPolicyAliasesAndManagement(t *testing.T) {
	for _, scenario := range []string{"disabled", "recursive", "policy", "pre-hook", "alias", "explain", "explain-json", "normalize", "doctor"} {
		t.Run(scenario, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				d := delegateFixture(s)
				if scenario == "recursive" {
					path := filepath.Join(s.root, "relay")
					if err := os.WriteFile(path, []byte("#!/bin/sh\nexec gh \"$@\"\n"), 0700); err != nil {
						t.Fatal(err)
					}
					d.Command = []string{path}
					d.Env = nil
				}
				if scenario == "doctor" {
					d.Command = []string{"missing-relay"}
				}
				c.Delegates = []config.Delegate{d}
				if scenario == "policy" {
					c.Policy.Mode = "enforce"
					c.Policy.Rules = []config.Rule{{ID: "deny", Match: config.Match{Operations: config.StringList{"pr.view"}}, Decision: "deny"}}
				}
				if scenario == "pre-hook" {
					c.Policy.Mode = "enforce"
					h := s.hook("deny", "pre")
					h.Env["FAKEGH_HOOK_DECISION"] = "deny"
					c.Hooks = []config.Hook{h}
				}
			})
			args := []string{"pr", "view", "12", "-R", "acme/project"}
			switch scenario {
			case "disabled":
				s.env["GHOK_NO_DELEGATE"] = "1"
				s.gh(nil, args...).code(t, 0)
				s.onlyRecord(args, nil)
			case "recursive":
				s.gh(nil, args...).code(t, 0)
				s.onlyRecord(args, nil)
			case "policy", "pre-hook":
				s.gh(nil, args...).code(t, 77)
				s.absentRecord()
			case "alias":
				if err := os.WriteFile(filepath.Join(s.env["GH_CONFIG_DIR"], "config.yml"), []byte("aliases: {pv: pr view}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append([]string{"pv"}, args[2:]...)
				s.gh(nil, args...).code(t, 0)
				if got := s.delegateRecords(); len(got) != 1 || !reflect.DeepEqual(got[0].Argv, args) {
					t.Fatalf("alias argv not preserved: %+v", got)
				}
			case "doctor":
				r := s.management(nil, "doctor", "--json")
				r.code(t, 1)
				if !strings.Contains(string(r.stdout), "delegate:relay") || !strings.Contains(string(r.stdout), "missing-relay") {
					t.Fatalf("doctor ignored missing delegate: %s", r.stdout)
				}
			default:
				command := []string{scenario, "--"}
				if scenario == "explain-json" {
					command = []string{"explain", "--json", "--"}
				}
				r := s.management(nil, append(command, args...)...)
				r.code(t, 0)
				if scenario == "explain" {
					if !strings.Contains(string(r.stdout), "Delegate that WOULD run: relay") || !strings.Contains(string(r.stdout), "exit=125") {
						t.Fatalf("missing explain selection: %s", r.stdout)
					}
				} else {
					var report struct {
						Delegate *struct {
							ID string `json:"id"`
						} `json:"delegate"`
					}
					decodeJSON(t, r.stdout, &report)
					if report.Delegate == nil || report.Delegate.ID != "relay" {
						t.Fatalf("missing JSON selection: %s", r.stdout)
					}
				}
				s.absentRecord()
			}
			if scenario != "alias" && len(s.delegateRecords()) != 0 {
				t.Fatal("delegate ran during inspection/denial/guard")
			}
		})
	}
}

func TestDelegateAPIGET(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		d := delegateFixture(s)
		d.Match = config.Match{Effects: config.StringList{"read"}, Levels: config.StringList{"L2"}, Methods: config.StringList{"GET"}, Commands: config.StringList{"api"}, Repositories: config.StringList{"acme/project"}}
		d.Env["FAKEGH_EXIT"] = "125"
		c.Delegates = []config.Delegate{d}
		c.Hooks = []config.Hook{s.hook("pre", "pre"), s.hook("post", "post")}
	})
	input := []byte(`{"hello":"world"}`)
	args := []string{"api", "repos/acme/project/pulls/12", "-X", "GET", "--input", "-"}
	r := s.gh(input, args...)
	r.code(t, 0)
	want := append([]string(nil), args...)
	s.onlyRecord(want, input)
	if records := s.delegateRecords(); len(records) != 1 || !reflect.DeepEqual(records[0].Argv, want) {
		t.Fatalf("delegate missed final request: %+v", records)
	}
	if pre := s.hookInput("pre"); pre.Operations[0].Target.Repository != "acme/project" {
		t.Fatalf("pre saw wrong request: %+v", pre)
	}
}

func TestDelegateTransformBeforeSelectionAndFallback(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		d := delegateFixture(s)
		d.Match.Repositories = config.StringList{"acme/transformed"}
		d.Env["FAKEGH_EXIT"] = "125"
		c.Delegates = []config.Delegate{d}
		c.Transforms = []config.Transform{transformFixture(s, "target", map[string]any{"/operations/0/target/repository": "acme/transformed"})}
		c.Hooks = []config.Hook{s.hook("pre", "pre")}
	})
	args := []string{"pr", "view", "12", "-R", "acme/project"}
	s.gh(nil, args...).code(t, 0)
	want := append([]string(nil), args...)
	want[len(want)-2] = "--repo" // transforms use the canonical flag spelling
	want[len(want)-1] = "acme/transformed"
	s.onlyRecord(want, nil)
	if records := s.delegateRecords(); len(records) != 1 || !reflect.DeepEqual(records[0].Argv, want) {
		t.Fatalf("delegate missed transformed request: %+v", records)
	}
	if pre := s.hookInput("pre"); pre.Operations[0].Target.Repository != "acme/transformed" {
		t.Fatalf("pre saw wrong target: %+v", pre)
	}
}

func TestDelegateProjectTrust(t *testing.T) {
	s := newSandbox(t)
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("version: 1\ndelegates: [{id: project-relay, match: {effect: read, level: L1}, command: [%q], env: {FAKEGH_RECORD: %q}}]\n", fakeBinary, filepath.Join(s.root, "delegate.jsonl"))
	if err := os.WriteFile(filepath.Join(s.cwd, ".ghok.yaml"), []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"pr", "view", "12"}
	s.gh(nil, args...).code(t, 0)
	s.onlyRecord(args, nil)
	if len(s.delegateRecords()) != 0 {
		t.Fatal("untrusted project delegate ran")
	}
	if err := os.Remove(s.record); err != nil {
		t.Fatal(err)
	}
	s.management(nil, "config", "trust", "--hook", "project-relay", "--no-diff").code(t, 0)
	s.gh(nil, args...).code(t, 0)
	s.absentRecord()
	if len(s.delegateRecords()) != 1 {
		t.Fatal("trusted project delegate did not run")
	}
}

func TestDelegateFallbackAfterPartialStdin(t *testing.T) {
	s := newSandbox(t)
	path := filepath.Join(s.root, "decline")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nIFS= read -r line\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.configure(func(c *config.Config) {
		d := delegateFixture(s)
		d.Command = []string{path}
		c.Delegates = []config.Delegate{d}
	})
	input := bytes.Repeat([]byte("line\x00data\xff\n"), 50000)
	args := []string{"pr", "view", "12"}
	s.gh(input, args...).code(t, 0)
	s.onlyRecord(args, input)
}

func TestDelegateSignalPassthrough(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(fmt.Sprint(external), func(t *testing.T) {
			s := newSandbox(t)
			ready := filepath.Join(s.root, "ready")
			s.configure(func(c *config.Config) {
				d := delegateFixture(s)
				if external {
					d.Env["FAKEGH_READY"] = ready
					d.Env["FAKEGH_SLEEP"] = "15s"
				} else {
					d.Env["FAKEGH_SIGNAL"] = strconv.Itoa(int(syscall.SIGTERM))
				}
				c.Delegates = []config.Delegate{d}
				c.Hooks = []config.Hook{s.hook("post", "post")}
			})
			if !external {
				s.gh(nil, "pr", "view", "12").signaled(t, syscall.SIGTERM)
			} else {
				cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, "pr", "view", "12")
				defer cancel()
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
				deadline := time.Now().Add(10 * time.Second)
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("delegate never ready")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				outcome{err: cmd.Wait()}.signaled(t, syscall.SIGTERM)
			}
			s.absentRecord()
			if post := s.hookInput("post"); post.Result == nil || post.Result.Signal != "SIGTERM" {
				t.Fatalf("post signal: %+v", post.Result)
			}
		})
	}
}
