//go:build unix

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
)

func TestBodyFileDefaultStdinTimeoutReplaysLiveBinaryRemainder(t *testing.T) {
	s := newSandbox(t)
	hookReady := filepath.Join(s.root, "pre-input-recorded")
	s.configure(func(c *config.Config) {
		yes := true
		h := s.hook("pre", "pre")
		h.Enabled = &yes
		// The marker is written only after the complete hook-input file is closed.
		h.Env["FAKEGH_HOOK_READY"] = hookReady
		c.Hooks = []config.Hook{h}
		// Leave runtime.stdinTimeout at its default of three seconds.
	})

	args := []string{"pr", "create", "-R", "acme/project", "-t", "T", "--body-file", "-"}
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
	prefix := []byte("incomplete body prefix\n")
	// Larger than a pipe buffer, including NUL and invalid UTF-8, so a live
	// consumer must drain the pump rather than merely replay its saved prefix.
	remainder := bytes.Repeat([]byte{0, 255, 'l', 'a', 't', 'e', '\n', 254}, 32768)
	if _, err := writer.Write(prefix); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			// On any failed handshake/assertion, kill the command's process group,
			// release both pipe ends, and reap the command before sandbox cleanup.
			cancel()
			_ = writer.Close()
			_ = reader.Close()
			_ = cmd.Wait()
		}
	}()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	waitFile(t, hookReady)
	// The producer has not closed or sent its remainder. Thus the pre-hook
	// cannot be running because EOF completed capture, even on a slow machine.
	if elapsed := time.Since(started); elapsed < 3*time.Second {
		t.Fatalf("pre-hook ran after %v, before the default 3s stdin timeout", elapsed)
	}
	input := s.hookInput("pre")
	if input.Event != "pre" || !reflect.DeepEqual(input.Invocation.Argv, args) {
		t.Fatalf("unexpected pre-hook invocation: event=%q argv=%q", input.Event, input.Invocation.Argv)
	}
	if len(input.Operations) != 1 || input.Operations[0].Texts == nil {
		t.Fatalf("missing pre-hook texts: %+v", input.Operations)
	}
	want := model.Texts{
		Title: &model.Text{Value: "T", Source: "inline", Bytes: 1},
		Body:  &model.Text{Source: "stdin", Status: model.StdinTimeout},
	}
	if got := input.Operations[0].Texts; !reflect.DeepEqual(*got, want) {
		t.Fatalf("pre-hook texts=%+v / %+v / %+v, want title=%+v body=%+v and no notes", got.Title, got.Body, got.Notes, want.Title, want.Body)
	}
	if input.Operations[0].Parameters["bodyFile"] != true {
		t.Fatal("pre-hook lost bodyFile parameter")
	}

	// Release the producer only after the recorded hook input proves timeout.
	// Bound writes too: a broken live pump must fail instead of hanging here.
	if err := writer.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := writer.Write(remainder); err != nil || n != len(remainder) {
		t.Fatalf("writing late stdin: bytes=%d, want=%d, err=%v", n, len(remainder), err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait() // sandbox.command bounds the process group with a 30s context.
	waited = true
	outcome{err: err, stdout: stdout.Bytes(), stderr: stderr.Bytes()}.code(t, 0)
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("command did not finish within its 30s deadline: %v", elapsed)
	}
	s.onlyRecord(args, append(prefix, remainder...))
}
