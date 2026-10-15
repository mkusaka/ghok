//go:build unix

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hooksExpectConfig(t *testing.T, mode, phase, body string) {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("GHOK_CONFIG_DIR", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	text := fmt.Sprintf("version: 1\npolicy:\n  mode: %s\nhooks:\n  - id: expectation-test\n    phase: %s\n    command: [\"/bin/sh\", \"-c\", %q]\n", mode, phase, body)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func hooksExpectRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stderr.Close() }()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	code := Management(args, "test", "test")
	out, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out), string(errOut)
}

func TestHooksExpectAggregate(t *testing.T) {
	for _, tc := range []struct {
		name, mode, body, expect, decision string
		code                               int
		wouldDeny                          bool
	}{
		{"enforce-deny-match", "enforce", `printf '{"decision":"deny"}'`, "deny", "deny", 0, true},
		{"observe-deny-is-audit", "observe", `printf '{"decision":"deny"}'`, "audit", "audit", 0, true},
		{"observe-not-raw-deny", "observe", `printf '{"decision":"deny"}'`, "deny", "audit", 1, true},
		{"enforce-mismatch", "enforce", `printf '{"decision":"deny"}'`, "allow", "deny", 1, true},
		{"allow-match", "enforce", `printf '{"decision":"allow"}'`, "allow", "allow", 0, false},
		{"abstain-hook-effective-allow", "enforce", `printf '{"decision":"abstain"}'`, "allow", "allow", 0, false},
		{"abstain-is-valid-expectation", "enforce", `printf '{"decision":"abstain"}'`, "abstain", "allow", 1, false},
		{"error-match-overrides-failure", "enforce", "exit 19", "deny", "deny", 0, true},
		{"observe-error-match-overrides-failure", "observe", "exit 19", "audit", "audit", 0, true},
		{"legacy-deny", "enforce", `printf '{"decision":"deny"}'`, "", "deny", 1, true},
		{"legacy-observe", "observe", `printf '{"decision":"deny"}'`, "", "audit", 0, true},
		{"legacy-allow", "enforce", `printf '{"decision":"allow"}'`, "", "allow", 0, false},
		{"legacy-error", "observe", "exit 19", "", "audit", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hooksExpectConfig(t, tc.mode, "pre", tc.body)
			args := []string{"hooks", "test", "--no-resolve", "--json"}
			if tc.expect != "" {
				args = append(args, "--expect", tc.expect)
			}
			args = append(args, "--", "version")
			code, out, errOut := hooksExpectRun(t, args...)
			if code != tc.code {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, tc.code, out, errOut)
			}
			var report struct {
				Final struct {
					Decision  string `json:"decision"`
					WouldDeny bool   `json:"wouldDeny"`
				} `json:"final"`
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("invalid JSON stdout: %v: %s", err, out)
			}
			if report.Final.Decision != tc.decision || report.Final.WouldDeny != tc.wouldDeny {
				t.Fatalf("final=%+v", report.Final)
			}
		})
	}
}

func TestHooksExpectInvalid(t *testing.T) {
	for _, value := range []string{"", "invalid", "ALLOW", "would-deny"} {
		t.Run("value="+value, func(t *testing.T) {
			code, out, errOut := hooksExpectRun(t, "hooks", "test", "--expect="+value, "--", "version")
			if code != 2 || out != "" || !strings.Contains(errOut, "--expect must be allow, deny, abstain or audit") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errOut)
			}
		})
	}
}

func TestHooksExpectSyntheticExitCode(t *testing.T) {
	hooksExpectConfig(t, "enforce", "post", "exit 0")
	code, out, errOut := hooksExpectRun(t, "hooks", "test", "--no-resolve", "--phase", "post", "--exit-code", "17", "--expect", "allow", "--", "version")
	if code != 0 || errOut != "" || !strings.Contains(out, "synthetic result (exitCode=17)") || strings.Contains(out, "successful") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errOut)
	}
}
