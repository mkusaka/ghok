//go:build unix

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
)

func TestOversizedAnalysisStillReplaysEveryByte(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Runtime.StdinMaxBytes = 64
		c.Hooks = []config.Hook{s.hook("pre", "pre"), s.hook("post", "post")}
	})
	input := bytes.Repeat([]byte{0, 255, 'x', '\n'}, 512000)
	args := []string{"api", "graphql", "--input", "-"}
	result := s.gh(input, args...)
	result.code(t, 0)
	s.onlyRecord(args, input)
	if s.hookInput("post").Classification.MutationFree {
		t.Fatal("oversized unanalysed stdin claimed mutation-free")
	}
}

func TestStdinTimeoutReplaysPrefixAndLiveRemainder(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Runtime.StdinTimeout = config.Duration(2 * time.Second)
		c.Hooks = []config.Hook{s.hook("pre", "pre"), s.hook("post", "post")}
	})
	ready := filepath.Join(s.root, "child-started")
	s.env["FAKEGH_STARTED"] = ready
	args := []string{"api", "graphql", "--input", "-"}
	cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, args...)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	cmd.Stdin = reader
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	prefix, remainder := []byte{0, 'p', 255}, []byte{'r', 0, '\n', 254}
	if _, err := writer.Write(prefix); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitFile(t, ready) // The child starts only after analysis times out, not EOF.
	if _, err := writer.Write(remainder); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	result.code(t, 0)
	s.onlyRecord(args, append(prefix, remainder...))
}

func TestDisabledLoggingObserveFastPathPreservesUnanalysedStdin(t *testing.T) {
	s := newSandbox(t)
	input := []byte{0, 255, 'n', 'o', 't', ' ', 'J', 'S', 'O', 'N'}
	args := []string{"api", "graphql", "--input", "-"}
	result := s.gh(input, args...)
	result.code(t, 0)
	s.onlyRecord(args, input)
	if len(result.stdout) != 0 || len(result.stderr) != 0 {
		t.Fatal("pure passthrough emitted diagnostics")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl")); !os.IsNotExist(err) {
		t.Fatal("disabled logging created a log")
	}
}

func TestUnneededStdinAnalysisStartsGhBeforeEOF(t *testing.T) {
	for _, setup := range []string{"default", "observe-deny-policy", "post-only", "disabled-pre", "unrelated-pre", "enforce-audit-only"} {
		for _, source := range []string{"input", "field"} {
			t.Run(setup+"/"+source, func(t *testing.T) {
				s := newSandbox(t)
				s.configure(func(c *config.Config) {
					yes, no := true, false
					c.Logging.Enabled = &yes
					switch setup {
					case "observe-deny-policy":
						c.Policy.UnknownMutation = config.UnknownDeny
						c.Policy.Rules = []config.Rule{{ID: "deny", Decision: "deny"}}
					case "post-only":
						c.Hooks = []config.Hook{s.hook("post", "post")}
					case "disabled-pre":
						h := s.hook("pre", "pre")
						h.Enabled = &no
						c.Hooks = []config.Hook{h}
					case "unrelated-pre":
						h := s.hook("pre", "pre")
						h.Match.Commands = config.StringList{"pr merge"}
						c.Hooks = []config.Hook{h}
					case "enforce-audit-only":
						c.Policy.Mode = config.ModeEnforce
						c.Policy.Rules = []config.Rule{{ID: "audit", Decision: "audit"}}
					}
				})
				args := []string{"api", "graphql", "--input", "-"}
				prefix, remainder := []byte(`{"query":"query {`), []byte(` viewer { login } }"}`)
				if source == "field" {
					args = []string{"api", "graphql", "-F", "query=@-"}
					prefix, remainder = []byte("query {"), []byte(" viewer { login } }")
				}
				ready := filepath.Join(s.root, "child-started")
				s.env["FAKEGH_STARTED"] = ready
				cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, args...)
				defer cancel()
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = reader.Close() }()
				defer func() { _ = writer.Close() }()
				cmd.Stdin = reader
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if _, err := writer.Write(prefix); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				waitFile(t, ready) // Producer still holds the pipe open; no EOF yet.
				if _, err := writer.Write(remainder); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				result := outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}
				result.code(t, 0)
				s.onlyRecord(args, append(prefix, remainder...))
				logs := s.logs()
				if len(logs) != 1 || logs[0].Classification.MutationFree ||
					len(logs[0].Operations) != 1 || !strings.Contains(logs[0].Operations[0].Detail, "stdin not captured") {
					t.Fatalf("uncaptured request log=%+v", logs)
				}
			})
		}
	}
}

func TestStdinAnalysisCanRevealMatchingDenyRule(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		c.Policy.Rules = []config.Rule{{ID: "merge", Decision: "deny", Match: config.Match{Operations: config.StringList{"pr.merge.*"}}}}
	})
	input := []byte(`{"query":"mutation { mergePullRequest(input:{pullRequestId:\"PR_test_node\"}) { clientMutationId } }"}`)
	result := s.gh(input, "api", "graphql", "--input", "-")
	result.code(t, 77)
	s.absentRecord()
}
