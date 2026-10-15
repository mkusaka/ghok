//go:build unix

package e2e

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestHooksTestPrintInput(t *testing.T) {
	for _, tc := range []struct {
		name, hook, phase        string
		showSecrets, redactInput bool
		exitCode                 int
	}{
		{name: "default-masked", phase: "pre"},
		{name: "raw", phase: "pre", showSecrets: true},
		{name: "raw-hook", hook: "preview", phase: "pre", showSecrets: true},
		{name: "redacted-hook-raw-display", hook: "preview", phase: "pre", showSecrets: true, redactInput: true},
		{name: "redacted-hook-default-display", hook: "preview", phase: "pre", redactInput: true},
		{name: "post-synthetic", hook: "preview", phase: "post", exitCode: 17},
		{name: "generic-post-synthetic", phase: "post", exitCode: 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			secret := "learned-preview-" + "credential"
			s.env["GH_TOKEN"] = secret
			s.configure(func(c *config.Config) {
				h := s.hook("preview", tc.phase)
				h.RedactInput = tc.redactInput
				if tc.phase == "post" {
					h.Match.Results = config.StringList{"failure"}
					h.Match.ExitCodes = []int{17}
				}
				c.Hooks = []config.Hook{h}
			})
			// Any attempt to prepare a nested hook/API token must fail, even if
			// cleanup would otherwise hide the token file after execution.
			state := filepath.Join(filepath.Dir(s.cfg), "state")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, "nested"), []byte("no nested tokens"), 0600); err != nil {
				t.Fatal(err)
			}
			flags := []string{"hooks", "test", "--print-input", "--phase", tc.phase}
			if tc.showSecrets {
				flags = append(flags, "--show-secrets")
			}
			if tc.hook != "" {
				flags = append(flags, "--hook", tc.hook)
			}
			if tc.phase == "post" {
				flags = append(flags, "--exit-code", "17")
			}
			body := secret + " and " + secret
			args := []string{"pr", "create", "-R", "acme/project", "-t", "ordinary title", "-b", body}
			result := s.management(nil, append(append(flags, "--"), args...)...)
			result.code(t, 0)
			var input protocol.Input
			decodeJSON(t, result.stdout, &input)
			if input.SchemaVersion != 1 || input.Event != tc.phase || input.EventID == "" || input.HookID != tc.hook || input.Invocation.Via != "shim" {
				t.Fatalf("invalid preview envelope: %+v", input)
			}
			wantCwd, err := filepath.EvalSymlinks(s.cwd)
			if err != nil {
				t.Fatal(err)
			}
			if input.Invocation.Cwd != wantCwd || input.Invocation.RealGh != s.real || input.Invocation.TokenKind != "unknown" || input.Policy == nil || len(input.Operations) != 1 {
				t.Fatalf("missing hook input context: %+v", input)
			}
			wantArgs := append([]string(nil), args...)
			wantBody := body
			if !tc.showSecrets || tc.redactInput {
				wantBody = "[REDACTED] and [REDACTED]"
				wantArgs[len(wantArgs)-1] = wantBody
			}
			if !reflect.DeepEqual(input.Invocation.Argv, wantArgs) || !reflect.DeepEqual(input.Invocation.Expanded, wantArgs) {
				t.Fatalf("preview arguments: argv=%q expanded=%q; want %q", input.Invocation.Argv, input.Invocation.Expanded, wantArgs)
			}
			op := input.Operations[0]
			if op.Family != "pr.create" || op.Target.Owner != "acme" || op.Target.Repository != "acme/project" || op.Texts == nil || op.Texts.Body == nil || op.Texts.Title == nil || op.Texts.Body.Value != wantBody || op.Texts.Body.Bytes != int64(len(body)) || op.Texts.Title.Value != "ordinary title" {
				t.Fatalf("preview operation: %+v", op)
			}
			wantRedactions, wantTextRedactions := 0, 0
			if tc.redactInput {
				wantRedactions, wantTextRedactions = 6, 2
			}
			if input.Redactions != wantRedactions || op.Texts.Body.Redactions != wantTextRedactions || op.Texts.Title.Redactions != 0 {
				t.Fatalf("display masking changed payload counters: total=%d body=%d title=%d", input.Redactions, op.Texts.Body.Redactions, op.Texts.Title.Redactions)
			}
			wantStderr := ""
			if !tc.showSecrets && !tc.redactInput {
				wantStderr = "6 values masked; use --show-secrets for the bytes the hook receives\n"
			}
			if string(result.stderr) != wantStderr {
				t.Fatalf("masking notice=%q, want %q", result.stderr, wantStderr)
			}
			if bytes.Contains(result.stdout, []byte(secret)) != (tc.showSecrets && !tc.redactInput) {
				t.Fatalf("incorrect preview secret visibility: %s", result.stdout)
			}
			if tc.phase == "post" {
				if input.Result == nil || input.Result.ExitCode != tc.exitCode || input.Result.DurationMs != 0 || input.Result.Stdout != nil || input.Result.Stderr != nil {
					t.Fatalf("synthetic post result=%+v", input.Result)
				}
			} else if input.Result != nil {
				t.Fatalf("pre preview has execution result: %+v", input.Result)
			}
			s.absentRecord()
			if _, err := os.Stat(filepath.Join(s.root, "preview.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preview executed hook: %v", err)
			}
		})
	}
}

func TestHooksTestPrintInputInvalidHook(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		h := s.hook("other-command", "pre")
		h.Match.Commands = config.StringList{"pr merge"}
		c.Hooks = []config.Hook{s.hook("post-only", "post"), h}
	})
	for _, tc := range []struct {
		flags   []string
		message string
	}{
		{[]string{"--print-input", "--hook", "missing"}, "unknown or non-matching hook ID"},
		{[]string{"--print-input", "--hook", "post-only"}, "unknown or non-matching hook ID"},
		{[]string{"--print-input", "--hook", "other-command"}, "unknown or non-matching hook ID"},
		{[]string{"--print-input", "--hook="}, "--hook requires a nonempty hook ID"},
		{[]string{"--hook", "post-only"}, "--hook requires --print-input"},
		{[]string{"--print-input", "--allow-api"}, "--allow-api cannot be combined with --print-input"},
	} {
		t.Run(strings.Join(tc.flags, "_"), func(t *testing.T) {
			result := s.management(nil, append(append([]string{"hooks", "test"}, tc.flags...), "--", "version")...)
			result.code(t, 2)
			if !bytes.Contains(result.stderr, []byte(tc.message)) || len(result.stdout) != 0 {
				t.Fatalf("invalid hook diagnostic: stdout=%s stderr=%s", result.stdout, result.stderr)
			}
		})
	}
	s.absentRecord()
}
