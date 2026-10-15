//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestHookTerminalOutput(t *testing.T) {
	for _, tc := range []struct {
		name, message, visible string
	}{
		{"controls", "review\x1b[31m\r\a\u0085\x7f\n\towners", `review\x1b[31m\x0d\x07\x85\x7f` + "\n\towners"},
		{"lines", strings.Repeat("review\n", 99) + "review", "… (61 more lines truncated)"},
		{"long-line", strings.Repeat("x", 10<<10), "…"},
		{"bytes", strings.Repeat(strings.Repeat("界", 400)+"\n", 9) + "end", "more lines truncated)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			response, err := json.Marshal(protocol.Response{Decision: protocol.DecisionDeny, Message: tc.message})
			if err != nil {
				t.Fatal(err)
			}
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				yes := true
				c.Logging.Enabled = &yes
				h := s.hook("review", config.PhasePre)
				h.Env["FAKEGH_HOOK_RESPONSE"] = string(response)
				c.Hooks = []config.Hook{h}
			})
			got := s.gh(nil, "version")
			got.code(t, 77)
			s.absentRecord()
			entry := bytes.TrimPrefix(got.stderr, []byte("ghok: denied by hook review\n"))
			assertTerminalBounds(t, entry)
			visible := strings.ReplaceAll(tc.visible, "\n", "\n    ")
			if !bytes.Contains(entry, []byte(visible)) || bytes.ContainsAny(entry, "\x1b\r\a\u0085\x7f") {
				t.Fatalf("unsafe or missing denial diagnostic: %q", entry)
			}
			logs := s.logs()
			if len(logs) != 1 || len(logs[0].Hooks) != 1 || logs[0].Hooks[0].Message != tc.message {
				t.Fatalf("terminal formatting altered structured log: %+v", logs)
			}

			text := s.management(nil, "hooks", "test", "--", "version")
			text.code(t, 1)
			_, message, found := bytes.Cut(text.stdout, []byte("Message: "))
			if !found || !bytes.Contains(message, []byte(tc.visible)) || bytes.ContainsAny(text.stdout, "\x1b\r\a\u0085\x7f") {
				t.Fatalf("unsafe or missing hooks test diagnostic: %q", text.stdout)
			}
			assertTerminalBounds(t, append([]byte("Message: "), message...))

			structured := s.management(nil, "hooks", "test", "--json", "--", "version")
			structured.code(t, 1)
			var result struct {
				Results []protocol.HookResult `json:"results"`
			}
			if err := json.Unmarshal(structured.stdout, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Results) != 1 || result.Results[0].Message != tc.message {
				t.Fatalf("terminal formatting altered JSON: %s", structured.stdout)
			}
		})
	}
}

func TestManagementTerminalDiagnostics(t *testing.T) {
	t.Run("explain", func(t *testing.T) {
		s := newSandbox(t)
		s.configure(func(c *config.Config) {
			c.Policy.Rules = []config.Rule{{ID: "review", Decision: "deny", Reason: "review\x1b[31m\r\n\towners"}}
		})
		for _, flags := range [][]string{{}, {"--show-secrets"}} {
			got := s.management(nil, append(append([]string{"explain"}, flags...), "--", "version")...)
			got.code(t, 0)
			if bytes.ContainsAny(got.stdout, "\x1b\r") || !bytes.Contains(got.stdout, []byte(`review\x1b[31m\x0d`+"\n\towners")) {
				t.Fatalf("unsafe explain diagnostic: %q", got.stdout)
			}
		}
	})
	t.Run("doctor", func(t *testing.T) {
		s := newSandbox(t)
		s.configure(func(c *config.Config) {
			h := s.hook("review", config.PhasePre)
			h.Command = []string{s.root + "/missing\x1b[31m\rtool"}
			c.Hooks = []config.Hook{h}
		})
		got := s.management(nil, "doctor")
		got.code(t, 1)
		if bytes.ContainsAny(got.stdout, "\x1b\r") || !bytes.Contains(got.stdout, []byte(`missing\x1b[31m\x0dtool`)) {
			t.Fatalf("unsafe doctor diagnostic: %q", got.stdout)
		}
		structured := s.management(nil, "doctor", "--json")
		structured.code(t, 1)
		var checks []struct{ Detail string }
		decodeJSON(t, structured.stdout, &checks)
		found := false
		for _, check := range checks {
			found = found || strings.Contains(check.Detail, "missing\x1b[31m\rtool")
		}
		if !found {
			t.Fatalf("terminal formatting altered doctor JSON: %s", structured.stdout)
		}
	})
	t.Run("validate", func(t *testing.T) {
		s := newSandbox(t)
		s.configure(func(c *config.Config) {
			rule := config.Rule{ID: "review\x1b[31m\r", Decision: "deny"}
			c.Policy.Rules = []config.Rule{rule, rule}
		})
		got := s.management(nil, "config", "validate")
		got.code(t, 1)
		if bytes.ContainsAny(got.stderr, "\x1b\r") || !bytes.Contains(got.stderr, []byte(`review\x1b[31m\x0d`)) {
			t.Fatalf("unsafe validation diagnostic: %q", got.stderr)
		}
	})
	t.Run("print-input", func(t *testing.T) {
		s := newSandbox(t)
		body := "\x1b[31m\r\n\t" + strings.Repeat("body\n", 100) + strings.Repeat("x", 10<<10)
		got := s.management(nil, "hooks", "test", "--print-input", "--", "pr", "create", "-R", "acme/project", "-t", "title", "-b", body)
		got.code(t, 0)
		var input protocol.Input
		decodeJSON(t, got.stdout, &input)
		if len(input.Operations) != 1 || input.Operations[0].Texts == nil || input.Operations[0].Texts.Body == nil || input.Operations[0].Texts.Body.Value != body {
			t.Fatalf("terminal formatting altered print-input JSON: %s", got.stdout)
		}
	})
}

func assertTerminalBounds(t *testing.T, text []byte) {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(text, []byte("\n")), []byte("\n"))
	if len(text) > 4<<10 || len(lines) > 40 || !utf8.Valid(text) {
		t.Fatalf("terminal entry exceeds bounds: %d bytes, %d lines, valid UTF-8=%t", len(text), len(lines), utf8.Valid(text))
	}
	for _, line := range lines {
		if utf8.RuneCount(line) > 500 {
			t.Fatalf("terminal line exceeds 500 characters: %d", utf8.RuneCount(line))
		}
	}
}
