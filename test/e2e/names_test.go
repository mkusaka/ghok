//go:build unix

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
)

func TestNamesHooksTestNativeStdin(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		h := s.hook("comment-input", "pre")
		h.Match.Operations = config.StringList{"pr.comment.create"}
		c.Hooks = []config.Hook{h}
	})
	body := "comment from redirected stdin\nsecond line\n"
	path := filepath.Join(s.root, "body.md")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	cmd, cancel := s.command(ghokBinary, nil, "hooks", "test", "--", "pr", "comment", "1", "--body-file", "-")
	defer cancel()
	cmd.Stdin = file
	output, err := cmd.CombinedOutput()
	outcome{err: err, stderr: output}.code(t, 0)
	op := s.hookInput("comment-input").Operations[0]
	want := model.Text{Value: body, Source: "stdin", Bytes: int64(len(body))}
	if op.Action != "pr.comment.create" || op.Target.Kind != "pullRequest" || op.Texts == nil || op.Texts.Body == nil || *op.Texts.Body != want {
		t.Fatalf("hooks test comment input = %+v, want body %+v", op, want)
	}
	s.absentRecord()
}

func TestNamesNormalizeNativeStdinWithoutPolicy(t *testing.T) {
	s := newSandbox(t)
	body := []byte("release notes from stdin\n")
	out := s.management(body, "normalize", "--", "release", "create", "v1", "--notes-file", "-")
	out.code(t, 0)
	var result model.Result
	if err := json.Unmarshal(out.stdout, &result); err != nil {
		t.Fatalf("normalize output: %v: %s", err, out.stdout)
	}
	if len(result.Operations) != 1 || result.Operations[0].Texts == nil || result.Operations[0].Texts.Notes == nil {
		t.Fatalf("missing notes: %+v", result)
	}
	if got := *result.Operations[0].Texts.Notes; got != (model.Text{Value: string(body), Source: "stdin", Bytes: int64(len(body))}) {
		t.Fatalf("normalize stdin notes = %+v", got)
	}
	s.absentRecord()
}

func TestNamesMatchingCommentsSafely(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"native-pr", []string{"pr", "comment", "1", "--body", "body", "-R", "o/r"}},
		{"native-issue", []string{"issue", "comment", "1", "--body", "body", "-R", "o/r"}},
		{"rest-polymorphic", []string{"api", "repos/o/r/issues/1/comments", "-f", "body=body"}},
		{"graphql-polymorphic", []string{"api", "graphql", "-f", `query=mutation { addComment(input:{subjectId:"opaque",body:"body"}) { clientMutationId } }`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = config.ModeEnforce
				c.Policy.Rules = []config.Rule{{
					ID: "comment-writes", Decision: "deny",
					Match: config.Match{Operations: config.StringList{"comment.*", "pr.comment.*", "issue.comment.*"}, Effects: config.StringList{"write"}},
				}}
			})
			s.gh(nil, tc.args...).code(t, 1)
			s.absentRecord()
		})
	}
}
