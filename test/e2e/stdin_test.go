//go:build unix

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
)

func TestOversizedAnalysisStillReplaysEveryByte(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) { c.Runtime.StdinMaxBytes = 64; c.Hooks = []config.Hook{s.hook("post", "post")} })
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
		c.Hooks = []config.Hook{s.hook("post", "post")}
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
	defer reader.Close()
	defer writer.Close()
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
