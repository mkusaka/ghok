//go:build unix

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

func transformFixture(s *sandbox, id string, set map[string]any) config.Transform {
	s.t.Helper()
	response, err := json.Marshal(map[string]any{"schemaVersion": 1, "set": set})
	if err != nil {
		s.t.Fatal(err)
	}
	return config.Transform{ID: id, Command: []string{fakeBinary}, Env: map[string]string{
		"FAKEGH_MODE": "hook", "FAKEGH_HOOK_RESPONSE": string(response),
		"FAKEGH_HOOK_RECORD": filepath.Join(s.root, id+".json"),
	}}
}

func TestTransformsWriteback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		input, file string
		set         map[string]any
		body        string
		labels      []string
	}{
		{name: "inline-comment", args: []string{"issue", "comment", "12", "-R", "acme/project", "--body", "original"}, set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
		{name: "body-file", args: []string{"issue", "comment", "12", "-R", "acme/project", "--body-file", "body.txt"}, file: "original", set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
		{name: "body-stdin", args: []string{"issue", "comment", "12", "-R", "acme/project", "--body-file", "-"}, input: "original", set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
		{name: "label-replace", args: []string{"pr", "edit", "12", "-R", "acme/project", "--add-label", "old"}, set: map[string]any{"/operations/0/parameters/addLabel": []string{"new", "reviewed"}}, labels: []string{"new", "reviewed"}},
		{name: "label-insert", args: []string{"pr", "edit", "12", "-R", "acme/project", "--title", "title"}, set: map[string]any{"/operations/0/parameters/addLabel": []string{"new"}}, labels: []string{"new"}},
		{name: "create-label-replace", args: []string{"pr", "create", "-R", "acme/project", "--title", "title", "--body", "body", "--label", "old"}, set: map[string]any{"/operations/0/parameters/label": []string{"new", "reviewed"}}},
		{name: "create-label-insert", args: []string{"pr", "create", "-R", "acme/project", "--title", "title", "--body", "body"}, set: map[string]any{"/operations/0/parameters/label": []string{"new"}}},
		{name: "rest-field", args: []string{"api", "repos/acme/project/issues/12/comments", "-F", "body=original"}, set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
		{name: "rest-input-stdin", args: []string{"api", "repos/acme/project/issues/12/comments", "--input", "-"}, input: `{"body":"original"}`, set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
		{name: "graphql-variable", args: []string{"api", "graphql", "-f", `query=mutation($input:AddCommentInput!){addComment(input:$input){clientMutationId}}`, "-f", "input[subjectId]=I_test", "-f", "input[body]=original"}, set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
		{name: "graphql-input-stdin", args: []string{"api", "graphql", "--input", "-"}, input: `{"query":"mutation($input:AddCommentInput!){addComment(input:$input){clientMutationId}}","variables":{"input":{"subjectId":"I_test","body":"original"}}}`, set: map[string]any{"/operations/0/texts/body/value": "changed"}, body: "changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.env["FAKEGH_READ_REQUEST"] = "1"
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(s.cwd, "body.txt"), []byte(tc.file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s.configure(func(c *config.Config) {
				c.Transforms = []config.Transform{transformFixture(s, "rewrite", tc.set)}
				c.Hooks = []config.Hook{s.hook("inspect", "pre")}
			})
			s.gh([]byte(tc.input), tc.args...).code(t, 0)
			in := s.hookInput("inspect")
			if len(in.Operations) != 1 {
				t.Fatalf("operations=%+v", in.Operations)
			}
			op := in.Operations[0]
			if tc.body != "" && (op.Texts == nil || op.Texts.Body == nil || op.Texts.Body.Value != tc.body) {
				t.Fatalf("body not transformed: %+v", op.Texts)
			}
			if tc.labels != nil {
				got, _ := json.Marshal(op.Parameters["addLabel"])
				want, _ := json.Marshal(tc.labels)
				if string(got) != string(want) {
					t.Fatalf("labels=%s want=%s", got, want)
				}
			}
			if strings.HasPrefix(tc.name, "create-label-") {
				got, _ := json.Marshal(op.Parameters["label"])
				want, _ := json.Marshal(tc.set["/operations/0/parameters/label"])
				if string(got) != string(want) {
					t.Fatalf("create labels=%s want=%s", got, want)
				}
			}
			records := s.records()
			if len(records) != 1 || reflect.DeepEqual(records[0].Argv, tc.args) && records[0].StdinLength == len(tc.input) {
				t.Fatalf("request was not rewritten: %+v", records)
			}
			if tc.name == "inline-comment" {
				want := append([]string(nil), tc.args...)
				want[len(want)-1] = tc.body
				s.onlyRecord(want, nil)
			}
			if tc.name == "rest-field" && (strings.Contains(strings.Join(records[0].Argv, " "), "-F") || !strings.Contains(strings.Join(records[0].Argv, " "), "body=changed")) {
				t.Fatalf("typed field not rewritten raw: %q", records[0].Argv)
			}
			if tc.file != "" {
				original, err := os.ReadFile(filepath.Join(s.cwd, "body.txt"))
				if err != nil || string(original) != tc.file {
					t.Fatalf("original file changed: %q %v", original, err)
				}
			}
			assertTransformRequestFiles(t, s, tc.body)
			if tc.name == "body-file" {
				data, err := os.ReadFile(s.record)
				if err != nil {
					t.Fatal(err)
				}
				var rec struct {
					RequestFiles map[string]json.RawMessage `json:"requestFiles"`
				}
				if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil || len(rec.RequestFiles) == 0 {
					t.Fatalf("file transport was not observed: %s (%v)", data, err)
				}
			}
		})
	}
}

func assertTransformRequestFiles(t *testing.T, s *sandbox, body string) {
	t.Helper()
	data, err := os.ReadFile(s.record)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		RequestFiles map[string]struct {
			Content string `json:"content"`
			Mode    uint32 `json:"mode"`
		} `json:"requestFiles"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
		t.Fatal(err)
	}
	for path, file := range record.RequestFiles {
		if body != "" && !strings.Contains(file.Content, body) {
			t.Fatalf("request file content=%q", file.Content)
		}
		if file.Mode&0777 != 0600 {
			t.Fatalf("request file mode=%o", file.Mode)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary request remains: %s: %v", path, err)
		}
	}
}

func TestTransformsFailureAndRollback(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		for _, onError := range []string{"deny", "allow"} {
			t.Run(mode+"/"+onError, func(t *testing.T) {
				s := newSandbox(t)
				s.configure(func(c *config.Config) {
					c.Policy.Mode = mode
					bad := transformFixture(s, "unsupported", map[string]any{"/operations/0/parameters/notEditable": "bad"})
					bad.OnError = onError
					c.Transforms = []config.Transform{transformFixture(s, "first", map[string]any{"/operations/0/texts/body/value": "changed"}), bad, transformFixture(s, "never", nil)}
					c.Hooks = []config.Hook{s.hook("inspect", "pre")}
				})
				args := []string{"issue", "comment", "12", "-R", "acme/project", "--body", "original"}
				result := s.gh(nil, args...)
				if onError == "deny" {
					result.code(t, 78)
					s.absentRecord()
				} else {
					result.code(t, 0)
					s.onlyRecord(args, nil)
					if got := s.hookInput("inspect").Operations[0].Texts.Body.Value; got != "original" {
						t.Fatalf("partial transform survived rollback: %q", got)
					}
				}
				if _, err := os.Stat(filepath.Join(s.root, "never.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("remaining transform ran: %v", err)
				}
			})
		}
	}
}

func TestTransformsPolicyAndManagement(t *testing.T) {
	for _, command := range []string{"exec", "explain", "test", "test-json", "print-input", "normalize"} {
		t.Run(command, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				c.Transforms = []config.Transform{transformFixture(s, "target", map[string]any{"/operations/0/target/repository": "acme/blocked"})}
				c.Policy.Rules = []config.Rule{{ID: "blocked", Match: config.Match{Repositories: config.StringList{"acme/blocked"}}, Decision: "deny"}}
				c.Hooks = []config.Hook{s.hook("inspect", "pre")}
			})
			args := []string{"issue", "comment", "12", "-R", "acme/project", "--body", "original"}
			var result outcome
			switch command {
			case "exec":
				result = s.gh(nil, args...)
				result.code(t, 77)
			case "explain":
				result = s.management(nil, append([]string{"explain", "--"}, args...)...)
				result.code(t, 0)
			case "test":
				result = s.management(nil, append([]string{"hooks", "test", "--expect", "deny", "--"}, args...)...)
				result.code(t, 0)
			case "test-json":
				result = s.management(nil, append([]string{"hooks", "test", "--json", "--expect", "deny", "--"}, args...)...)
				result.code(t, 0)
				var report struct {
					Transformed struct {
						Argv       []string `json:"argv"`
						Transforms []string `json:"transforms"`
						Operations []struct {
							Target struct {
								Repository string `json:"repository"`
							} `json:"target"`
						} `json:"operations"`
					} `json:"transformed"`
				}
				decodeJSON(t, result.stdout, &report)
				if len(report.Transformed.Transforms) != 1 || report.Transformed.Transforms[0] != "target" || len(report.Transformed.Operations) != 1 || report.Transformed.Operations[0].Target.Repository != "acme/blocked" || !strings.Contains(strings.Join(report.Transformed.Argv, " "), "acme/blocked") {
					t.Fatalf("missing transformed report: %s", result.stdout)
				}
			case "print-input":
				result = s.management(nil, append([]string{"hooks", "test", "--print-input", "--"}, args...)...)
				result.code(t, 0)
				var input protocol.Input
				decodeJSON(t, result.stdout, &input)
				if input.Operations[0].Target.Repository != "acme/blocked" || input.Policy == nil || input.Policy.Decision != "deny" {
					t.Fatalf("preview not transformed: %+v", input)
				}
			case "normalize":
				result = s.management(nil, append([]string{"normalize", "--"}, args...)...)
				result.code(t, 0)
				if strings.Contains(string(result.stdout), "acme/blocked") || !strings.Contains(string(result.stdout), "acme/project") {
					t.Fatalf("normalize changed: %s", result.stdout)
				}
			}
			if command == "explain" && (!strings.Contains(string(result.stdout), "blocked") || !strings.Contains(string(result.stdout), "deny")) {
				t.Fatalf("explain did not judge transformed target: %s", result.stdout)
			}
			s.absentRecord()
			_, err := os.Stat(filepath.Join(s.root, "target.json"))
			if command == "normalize" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("normalize ran transform: %v", err)
			}
			if command != "normalize" && err != nil {
				t.Fatalf("transform not run: %v", err)
			}
		})
	}
}

func TestTransformsUntrustedProjectIgnored(t *testing.T) {
	s := newSandbox(t)
	if err := os.Mkdir(filepath.Join(s.cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project := "version: 1\npolicy:\n  mode: enforce\ntransforms:\n  pre:\n    - id: untrusted\n      command: [\"" + s.real + "\"]\n      env:\n        FAKEGH_MODE: hook\n        FAKEGH_HOOK_EXIT: '1'\n      onError: deny\n"
	if err := os.WriteFile(filepath.Join(s.cwd, ".ghok.yaml"), []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"issue", "comment", "12", "-R", "acme/project", "--body", "original"}
	s.gh(nil, args...).code(t, 0)
	s.onlyRecord(args, nil)
}

func TestTransformSessionExample(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "attribute-session")
	build := exec.Command("go", "build", "-o", binary, "./examples/transforms/attribute-session")
	build.Dir = root
	build.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, output)
	}
	for _, session := range []string{"", "session-example"} {
		t.Run("session="+session, func(t *testing.T) {
			s := newSandbox(t)
			s.env["CLAUDE_CODE_SESSION_ID"] = session
			s.configure(func(c *config.Config) { c.Transforms = []config.Transform{{ID: "session", Command: []string{binary}}} })
			args := []string{"issue", "comment", "12", "-R", "acme/project", "--body", "original"}
			s.gh(nil, args...).code(t, 0)
			want := append([]string(nil), args...)
			if session != "" {
				want[len(want)-1] += "\n\nfrom session_id: " + session
			}
			s.onlyRecord(want, nil)
		})
	}
}

func TestTransformsLogOriginalAndAppliedRequest(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		yes := true
		c.Logging.Enabled = &yes
		c.Transforms = []config.Transform{transformFixture(s, "rewrite", map[string]any{"/operations/0/texts/body/value": "changed body"})}
	})
	s.gh(nil, "issue", "comment", "12", "-R", "acme/project", "--body", "original").code(t, 0)
	logs := s.logs()
	if len(logs) != 1 {
		t.Fatalf("logs=%+v", logs)
	}
	record := logs[0]
	if record.Argv[len(record.Argv)-1] != "[content: 8 bytes]" || record.Transformed == nil || record.Transformed.Texts["body"] != 12 || !reflect.DeepEqual(record.Transformed.Transforms, []string{"rewrite"}) {
		t.Fatalf("missing original/transformed evidence: %+v", record)
	}
	if got := record.Transformed.Argv[len(record.Transformed.Argv)-1]; got != "[content: 12 bytes]" {
		t.Fatalf("transformed body exposed or missing: %q", got)
	}
}
