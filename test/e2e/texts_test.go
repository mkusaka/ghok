//go:build unix

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
)

func TestTextsPreHookAndLogs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		file string
		want model.Texts
	}{
		{
			name: "inline",
			args: []string{"pr", "create", "-R", "acme/project", "-t", "T", "-b", "B"},
			want: model.Texts{Title: &model.Text{Value: "T", Source: "inline", Bytes: 1}, Body: &model.Text{Value: "B", Source: "inline", Bytes: 1}},
		},
		{
			name: "bundled",
			args: []string{"pr", "create", "-R", "acme/project", "-t", "private title", "-db", "private body"},
			want: model.Texts{Title: &model.Text{Value: "private title", Source: "inline", Bytes: 13}, Body: &model.Text{Value: "private body", Source: "inline", Bytes: 12}},
		},
		{
			name: "body-file",
			args: []string{"pr", "create", "-R", "acme/project", "-t", "file title", "--body-file", "body.md"},
			file: "file body\nsecond line\n",
			want: model.Texts{Title: &model.Text{Value: "file title", Source: "inline", Bytes: 10}, Body: &model.Text{Value: "file body\nsecond line\n", Source: "file", Path: "body.md", Bytes: 22}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				c.Hooks = []config.Hook{s.hook("pre", "pre")}
			})
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(s.cwd, "body.md"), []byte(tc.file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s.gh(nil, tc.args...).code(t, 0)
			s.onlyRecord(tc.args, nil)
			input := s.hookInput("pre")
			if len(input.Operations) != 1 || input.Operations[0].Texts == nil {
				t.Fatalf("missing normalized hook texts: %+v", input.Operations)
			}
			got := input.Operations[0].Texts
			if got.Title == nil || *got.Title != *tc.want.Title || got.Body == nil || *got.Body != *tc.want.Body {
				t.Fatalf("hook texts = %+v / %+v, want %+v / %+v", got.Title, got.Body, tc.want.Title, tc.want.Body)
			}
			if tc.file != "" && input.Operations[0].Parameters["bodyFile"] != true {
				t.Fatal("bodyFile parameter compatibility lost")
			}
			logs := s.logs()
			if len(logs) != 1 || len(logs[0].Operations) != 1 || logs[0].Operations[0].Texts == nil {
				t.Fatalf("missing logged texts: %+v", logs)
			}
			logged := logs[0].Operations[0].Texts
			for _, pair := range [][2]*model.Text{{tc.want.Title, logged.Title}, {tc.want.Body, logged.Body}} {
				original, safe := pair[0], pair[1]
				if safe == nil || safe.Value != fmt.Sprintf("[content: %d bytes]", original.Bytes) || safe.Source != original.Source || safe.Path != original.Path || safe.Bytes != original.Bytes {
					t.Fatalf("logged text = %+v, original = %+v", safe, original)
				}
			}
			logBytes, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{tc.want.Title.Value, tc.want.Body.Value} {
				if bytes.Contains(logBytes, []byte(`"value":"`+value+`"`)) {
					t.Fatalf("log contains text value: %s", logBytes)
				}
			}
		})
	}
}

func TestTextsPreserveCredentialRedaction(t *testing.T) {
	s := newSandbox(t)
	secret := "learned-" + "texts-credential"
	pattern := "ghp_" + strings.Repeat("s", 36)
	s.env["GH_TOKEN"] = secret
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
		h := s.hook("pre", "pre")
		h.RedactInput = true
		c.Hooks = []config.Hook{h}
	})
	body := "ordinary prose " + secret + " " + pattern
	args := []string{"pr", "create", "-R", "acme/project", "-t", "ordinary title", "-b", body}
	s.gh(nil, args...).code(t, 0)
	s.onlyRecord(args, nil)
	input := s.hookInput("pre")
	if len(input.Operations) != 1 || input.Operations[0].Texts == nil || input.Operations[0].Texts.Body == nil {
		t.Fatalf("missing hook text: %+v", input.Operations)
	}
	text := input.Operations[0].Texts.Body
	if text.Value != "ordinary prose [REDACTED] [REDACTED]" || text.Bytes != int64(len(body)) {
		t.Fatalf("hook text lost redaction or byte count: %+v", text)
	}
	for _, path := range []string{filepath.Join(s.root, "pre.json"), filepath.Join(filepath.Dir(s.cfg), "logs", "ghok.jsonl")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(pattern)) {
			t.Fatalf("credential exposed in %s", filepath.Base(path))
		}
	}
}

func TestTextsStdinCaptureAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "body-file", args: []string{"pr", "create", "-R", "acme/project", "-t", "T", "--body-file", "-"}},
		{name: "bundled-body-file", args: []string{"pr", "create", "-R", "acme/project", "-t", "T", "-dF-"}},
		{name: "notes-file", args: []string{"release", "create", "v1", "-R", "acme/project", "--notes-file=-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Hooks = []config.Hook{s.hook("pre", "pre")}
			})
			body := []byte("secret " + "body\n本文\x00")
			s.gh(body, tc.args...).code(t, 0)
			s.onlyRecord(tc.args, body)
			input := s.hookInput("pre")
			if len(input.Operations) != 1 || input.Operations[0].Texts == nil {
				t.Fatalf("missing hook texts: %+v", input.Operations)
			}
			text := input.Operations[0].Texts.Body
			if tc.name == "notes-file" {
				text = input.Operations[0].Texts.Notes
			}
			want := model.Text{Value: string(body), Source: "stdin", Bytes: int64(len(body))}
			if text == nil || *text != want {
				t.Fatalf("stdin text = %+v, want %+v", text, want)
			}
		})
	}
}

func TestTextsAnalysisLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		size   int
	}{
		{name: "stdin-at-limit", source: "pipe", size: 64},
		{name: "stdin-over-limit", source: "pipe", size: 65},
		{name: "stdin-live-remainder", source: "pipe", size: 256 * 1024},
		{name: "regular-stdin-offset", source: "regular", size: 65},
		{name: "file-at-limit", source: "file", size: 64},
		{name: "file-over-limit", source: "file", size: 65},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				yes := true
				c.Logging.Enabled = &yes
				c.Runtime.StdinMaxBytes, c.Runtime.FileMaxBytes = 64, 64
				c.Hooks = []config.Hook{s.hook("pre", "pre")}
			})
			body := bytes.Repeat([]byte("s"), tc.size)
			args := []string{"pr", "create", "-R", "acme/project", "-t", "T", "--body-file", "-"}
			replayed := body
			want := model.Text{Source: "stdin", Bytes: int64(len(body)), Value: string(body)}
			if tc.size > 64 {
				want.Value, want.Truncated, want.Status = "", true, model.StdinTooLarge
				if tc.source == "pipe" {
					want.Bytes = 0 // The unread stream's total is not known before gh starts.
				}
			}
			switch tc.source {
			case "pipe":
				s.gh(body, args...).code(t, 0)
			case "regular":
				path := filepath.Join(s.root, "stdin")
				prefix := []byte("already consumed\n")
				if err := os.WriteFile(path, append(prefix, body...), 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = file.Close() }()
				if _, err := file.Seek(int64(len(prefix)), 0); err != nil {
					t.Fatal(err)
				}
				cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, args...)
				defer cancel()
				cmd.Stdin = file
				output, err := cmd.CombinedOutput()
				outcome{err: err, stderr: output}.code(t, 0)
			case "file":
				args[len(args)-1] = "body.md"
				if err := os.WriteFile(filepath.Join(s.cwd, "body.md"), body, 0600); err != nil {
					t.Fatal(err)
				}
				replayed = nil
				want.Source, want.Path, want.Status = "file", "body.md", ""
				s.gh(nil, args...).code(t, 0)
			}
			s.onlyRecord(args, replayed)
			input := s.hookInput("pre")
			if len(input.Operations) != 1 || input.Operations[0].Texts == nil || input.Operations[0].Texts.Body == nil {
				t.Fatalf("missing text: %+v", input.Operations)
			}
			got := input.Operations[0].Texts.Body
			if *got != want {
				t.Fatalf("bounded text = %+v, want %+v", got, want)
			}
			logs := s.logs()
			if len(logs) != 1 || logs[0].Operations[0].Texts == nil {
				t.Fatalf("missing log text: %+v", logs)
			}
			logged := logs[0].Operations[0].Texts.Body
			want.Value = fmt.Sprintf("[content: %d bytes]", len(want.Value))
			if logged == nil || *logged != want {
				t.Fatalf("logged text = %+v, want %+v", logged, want)
			}
		})
	}
}

func TestTextsUncapturedStdinReplay(t *testing.T) {
	for _, setup := range []string{"post-only", "disabled-pre", "unrelated-command", "unrelated-operation", "unrelated-effect", "unrelated-level", "unrelated-repository", "timeout"} {
		t.Run(setup, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				h := s.hook("pre", "pre")
				switch setup {
				case "post-only":
					h.Phase = config.PhasePost
				case "disabled-pre":
					no := false
					h.Enabled = &no
				case "unrelated-command":
					h.Match.Commands = config.StringList{"pr merge"}
				case "unrelated-operation":
					h.Match.Operations = config.StringList{"pr.merge.*"}
				case "unrelated-effect":
					h.Match.Effects = config.StringList{"read"}
				case "unrelated-level":
					h.Match.Levels = config.StringList{"L2"}
				case "unrelated-repository":
					h.Match.Repositories = config.StringList{"other/project"}
				case "timeout":
					c.Runtime.StdinTimeout = config.Duration(50 * time.Millisecond)
				}
				c.Hooks = []config.Hook{h, s.hook("post", "post")}
			})
			ready := filepath.Join(s.root, "child-started")
			s.env["FAKEGH_STARTED"] = ready
			args := []string{"pr", "create", "-R", "acme/project", "-t", "T", "-F", "-"}
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
			prefix, remainder := []byte("secret "), []byte("body\n\x00")
			if _, err := writer.Write(prefix); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			waitFile(t, ready) // No EOF: unneeded capture must not wait for the producer.
			if _, err := writer.Write(remainder); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			outcome{err: cmd.Wait(), stdout: stdout.Bytes(), stderr: stderr.Bytes()}.code(t, 0)
			s.onlyRecord(args, append(prefix, remainder...))
			input := s.hookInput("post")
			if len(input.Operations) != 1 || input.Operations[0].Texts == nil || input.Operations[0].Texts.Body == nil {
				t.Fatalf("missing post text: %+v", input.Operations)
			}
			want := model.Text{Source: "stdin", Status: model.StdinNotCaptured}
			if setup == "timeout" {
				want.Status = model.StdinTimeout
			}
			if got := input.Operations[0].Texts.Body; *got != want {
				t.Fatalf("unavailable text = %+v, want %+v", got, want)
			}
		})
	}
}

func TestTextsTTYStdinIsNeverRead(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = config.ModeEnforce
		h := s.hook("pre", "pre")
		h.Env["FAKEGH_HOOK_DECISION"] = "deny"
		c.Hooks = []config.Hook{h}
	})
	terminal, slave, err := pty.Open()
	if err != nil {
		if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("PTY unsupported: %v", err)
		}
		t.Fatal(err)
	}
	defer func() { _ = terminal.Close() }()
	defer func() { _ = slave.Close() }()
	args := []string{"pr", "create", "-R", "acme/project", "-t", "T", "-F", "-"}
	cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, args...)
	defer cancel()
	cmd.Stdin = slave
	output, err := cmd.CombinedOutput() // No terminal bytes or EOF: a pre-read would block.
	outcome{err: err, stderr: output}.code(t, 1)
	s.absentRecord()
	input := s.hookInput("pre")
	if !input.Invocation.StdinTTY || len(input.Operations) != 1 || input.Operations[0].Texts == nil || input.Operations[0].Texts.Body == nil {
		t.Fatalf("missing terminal text metadata: %+v", input)
	}
	want := model.Text{Source: "stdin", Status: model.StdinTTY}
	if got := input.Operations[0].Texts.Body; *got != want {
		t.Fatalf("terminal text = %+v, want %+v", got, want)
	}
}
