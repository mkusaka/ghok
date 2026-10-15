//go:build unix

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

// A malfunctioning hook (onError: deny) shows the tail of its stderr in the
// deny output so the cause is visible without opening the log.
func TestHookMalfunctionShowsStderrTail(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		h := s.hook("gate", "pre")
		h.OnError = config.OnErrorDeny
		h.Env["FAKEGH_HOOK_EXIT"] = "127"
		setBytes(h.Env, "FAKEGH_HOOK_STDERR_B64", []byte("booting\nenv: bun: No such file or directory\n"))
		c.Hooks = []config.Hook{h}
	})
	result := s.gh(nil, "pr", "close", "1", "-R", "o/r")
	result.code(t, 78)
	s.absentRecord()
	out := string(result.stderr)
	if !strings.HasPrefix(out, "ghok: denied by hook gate (error: exit 127)") || !strings.Contains(out, "stderr: env: bun: No such file or directory") {
		t.Fatalf("deny output:\n%s", out)
	}
}

// Hooks learn the entry point only out of band (GHOK_EVENT_SOURCE); the input
// JSON is identical to the shim's, and --print-input keeps stdout pure JSON.
func TestEventSourceAndPrintInputStreams(t *testing.T) {
	s := newSandbox(t)
	record := filepath.Join(s.root, "source.txt")
	script := filepath.Join(s.root, "source-hook")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' \"$GHOK_EVENT_SOURCE\" >> \""+record+"\"\nprintf ' ' >> \""+record+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.configure(func(c *config.Config) {
		c.Hooks = []config.Hook{{ID: "source", Phase: config.PhasePre, Command: []string{script}}}
	})
	s.gh(nil, "pr", "view", "1", "-R", "o/r").code(t, 0)
	s.management(nil, "hooks", "test", "--", "pr", "view", "1", "-R", "o/r").code(t, 0)
	data, err := os.ReadFile(record)
	if err != nil || strings.TrimSpace(string(data)) != "shim test" {
		t.Fatalf("GHOK_EVENT_SOURCE values = %q, %v", data, err)
	}
	result := s.management(nil, "hooks", "test", "--print-input", "--", "pr", "view", "1", "-R", "o/r")
	result.code(t, 0)
	var input map[string]any
	if err := json.Unmarshal(result.stdout, &input); err != nil {
		t.Fatalf("stdout must be the hook input JSON only: %v\n%s", err, result.stdout)
	}
	if via := input["invocation"].(map[string]any)["via"]; via != "shim" {
		t.Fatalf("hook input via = %v, want shim (identical to the real input)", via)
	}
	if !strings.Contains(string(result.stderr), "source: test") {
		t.Fatalf("the test-source note belongs on stderr: %s", result.stderr)
	}
}
