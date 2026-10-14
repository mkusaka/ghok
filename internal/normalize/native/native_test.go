package native

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestNormalizeSelectors(t *testing.T) {
	tests := []struct {
		name               string
		argv               []string
		env                map[string]string
		repo, host, branch string
		number             int
		resolution         model.Resolution
	}{
		{name: "root repo", argv: []string{"-R", "org/repo", "pr", "merge", "123"}, repo: "org/repo", host: "github.com", number: 123, resolution: model.ResolutionComplete},
		{name: "parent repo", argv: []string{"pr", "-Rorg/repo", "merge", "#123"}, repo: "org/repo", host: "github.com", number: 123, resolution: model.ResolutionComplete},
		{name: "leaf repo equals", argv: []string{"pr", "merge", "-R=org/repo", "123"}, repo: "org/repo", host: "github.com", number: 123, resolution: model.ResolutionComplete},
		{name: "long attached", argv: []string{"pr", "merge", "--repo=ghe.example/org/repo", "123"}, repo: "org/repo", host: "ghe.example", number: 123, resolution: model.ResolutionComplete},
		{name: "repo URL", argv: []string{"pr", "merge", "123", "--repo", "https://ghe.example/org/repo.git"}, repo: "org/repo", host: "ghe.example", number: 123, resolution: model.ResolutionComplete},
		{name: "PR URL", argv: []string{"pr", "merge", "https://ghe.example/o/r/pull/19/files?x=1#diff"}, repo: "o/r", host: "ghe.example", number: 19, resolution: model.ResolutionComplete},
		{name: "issue URL", argv: []string{"issue", "close", "https://ghe.example/o/r/issues/20#comment"}, repo: "o/r", host: "ghe.example", number: 20, resolution: model.ResolutionComplete},
		{name: "branch", argv: []string{"pr", "merge", "feature-x"}, env: map[string]string{"GH_REPO": "o/r"}, repo: "o/r", host: "github.com", branch: "feature-x", resolution: model.ResolutionDeferred},
		{name: "missing", argv: []string{"pr", "merge"}, resolution: model.ResolutionDeferred},
		{name: "env repo", argv: []string{"pr", "merge", "4"}, env: map[string]string{"GH_REPO": "o/r", "GH_HOST": "ghe.example"}, repo: "o/r", host: "ghe.example", number: 4, resolution: model.ResolutionComplete},
		{name: "explicit precedence", argv: []string{"pr", "merge", "4", "-R", "a/b"}, env: map[string]string{"GH_REPO": "env.example/o/r", "GH_HOST": "ghe.example"}, repo: "a/b", host: "ghe.example", number: 4, resolution: model.ResolutionComplete},
		{name: "URL precedence", argv: []string{"pr", "merge", "https://url.example/a/b/pull/7", "-R", "other.example/o/r"}, repo: "a/b", host: "url.example", number: 7, resolution: model.ResolutionComplete},
		{name: "host without repo", argv: []string{"pr", "merge", "4"}, env: map[string]string{"GH_HOST": "ghe.example"}, host: "ghe.example", number: 4, resolution: model.ResolutionDeferred},
		{name: "repo delete positional", argv: []string{"repo", "delete", "o/r", "--yes"}, repo: "o/r", host: "github.com", resolution: model.ResolutionComplete},
		{name: "repo view URL", argv: []string{"repo", "view", "https://ghe.example/o/r"}, repo: "o/r", host: "ghe.example", resolution: model.ResolutionComplete},
		{name: "options end", argv: []string{"pr", "merge", "-R", "o/r", "--", "feature-x"}, repo: "o/r", host: "github.com", branch: "feature-x", resolution: model.ResolutionDeferred},
		{name: "repo view missing", argv: []string{"repo", "view"}, resolution: model.ResolutionDeferred},
		{name: "repo view env", argv: []string{"repo", "view"}, env: map[string]string{"GH_REPO": "ghe.example/o/r"}, repo: "o/r", host: "ghe.example", resolution: model.ResolutionComplete},
		{name: "URL bypasses invalid env", argv: []string{"pr", "merge", "https://ghe.example/o/r/pull/9"}, env: map[string]string{"GH_REPO": "invalid"}, repo: "o/r", host: "ghe.example", number: 9, resolution: model.ResolutionComplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Normalize(model.Invocation{Env: tt.env}, tt.argv)
			op := r.Operations[0]
			target := op.Target
			if target.Repository != tt.repo || target.Host != tt.host || target.HeadBranch != tt.branch {
				t.Fatalf("target=%+v", target)
			}
			if tt.number != 0 && (target.Number == nil || *target.Number != tt.number) {
				t.Fatalf("number=%v want %d", target.Number, tt.number)
			}
			if tt.number == 0 && target.Number != nil {
				t.Fatalf("unexpected number %d", *target.Number)
			}
			if r.Classification.Resolution != tt.resolution {
				t.Fatalf("resolution=%s unresolved=%+v", r.Classification.Resolution, op.Unresolved)
			}
		})
	}
}

func TestNormalizeMergeIntent(t *testing.T) {
	for _, tt := range []struct {
		flags          []string
		action, method string
		delete, admin  bool
	}{
		{action: "pr.merge.request"},
		{flags: []string{"--auto", "--squash", "-d"}, action: "pr.merge.auto.enable", method: "squash", delete: true},
		{flags: []string{"--disable-auto"}, action: "pr.merge.auto.disable"},
		{flags: []string{"--admin", "--merge"}, action: "pr.merge.direct", method: "merge", admin: true},
		{flags: []string{"--rebase", "--auto=false", "--delete-branch=false"}, action: "pr.merge.request", method: "rebase"},
		{flags: []string{"-ds"}, action: "pr.merge.request", method: "squash", delete: true},
	} {
		r := Normalize(model.Invocation{}, append([]string{"pr", "merge", "123", "-R", "o/r"}, tt.flags...))
		op := r.Operations[0]
		if op.Family != "pr.merge" || op.Action != tt.action || op.Effect != model.EffectWrite || r.Classification.Confidence != model.ConfidenceExact || r.Classification.MutationFree {
			t.Fatalf("bad merge intent: %+v", r)
		}
		if op.Parameters["method"] != tt.method || op.Parameters["deleteBranch"] != tt.delete || op.Parameters["admin"] != tt.admin {
			t.Fatalf("parameters=%v", op.Parameters)
		}
		if !strings.Contains(op.Detail, "decided by the server") {
			t.Fatalf("missing intent caveat: %s", op.Detail)
		}
	}
	r := Normalize(model.Invocation{}, []string{"pr", "merge", "-R", "o/r", "-tSubject", "--match-head-commit=abc", "-A", "a@example.com", "--body", "not retained", "123"})
	p := r.Operations[0].Parameters
	if p["subject"] != "Subject" || p["matchHeadCommit"] != "abc" || p["authorEmail"] != "a@example.com" || p["body"] != true {
		t.Fatalf("parameters=%v", p)
	}
}

func TestNormalizeFlagSafety(t *testing.T) {
	for _, tt := range []struct {
		argv       []string
		family     string
		effect     model.Effect
		free       bool
		confidence model.Confidence
	}{
		{[]string{"pr", "merge", "123", "--future", "arg"}, "pr.merge", model.EffectWrite, false, model.ConfidenceHeuristic},
		{[]string{"pr", "view", "123", "--future=arg"}, "pr.view", model.EffectRead, false, model.ConfidenceHeuristic},
		{[]string{"future", "verb"}, "unknown", model.EffectUnknown, false, model.ConfidenceNone},
		{[]string{"pr", "future"}, "unknown", model.EffectUnknown, false, model.ConfidenceNone},
		{[]string{"extension", "exec", "my-extension"}, "extension.exec", model.EffectUnknown, false, model.ConfidenceExact},
		{[]string{"extension", "exec", "my-extension", "--help"}, "extension.exec", model.EffectUnknown, false, model.ConfidenceExact},
		{[]string{"future", "--help"}, "unknown", model.EffectUnknown, false, model.ConfidenceNone},
		{[]string{"pr", "merge", "123", "--help"}, "help", model.EffectRead, true, model.ConfidenceExact},
		{[]string{"pr", "merge", "-h"}, "help", model.EffectRead, true, model.ConfidenceExact},
		{[]string{"--version"}, "help", model.EffectRead, true, model.ConfidenceExact},
		{[]string{"help", "pr", "merge"}, "help", model.EffectRead, true, model.ConfidenceExact},
		{[]string{"pr", "merge", "123", "--body", "--help"}, "pr.merge", model.EffectWrite, false, model.ConfidenceExact},
		{[]string{"pr", "merge", "--", "--help"}, "pr.merge", model.EffectWrite, false, model.ConfidenceExact},
		{[]string{"auth", "login", "-h", "ghe.example", "--with-token"}, "auth.login", model.EffectWrite, false, model.ConfidenceExact},
		{[]string{"pr", "merge", "--auto=not-bool"}, "pr.merge", model.EffectWrite, false, model.ConfidenceHeuristic},
		{[]string{"pr", "merge", "-R"}, "pr.merge", model.EffectWrite, false, model.ConfidenceHeuristic},
	} {
		t.Run(strings.Join(tt.argv, " "), func(t *testing.T) {
			r := Normalize(model.Invocation{}, tt.argv)
			op := r.Operations[0]
			if op.Family != tt.family || op.Effect != tt.effect || r.Classification.MutationFree != tt.free || r.Classification.Confidence != tt.confidence {
				t.Fatalf("result=%+v", r)
			}
		})
	}
	r := Normalize(model.Invocation{}, []string{"pr", "merge", "123", "--future", "value"})
	if r.Operations[0].Target.Number != nil || r.Classification.Resolution != model.ResolutionPartial {
		t.Fatalf("ambiguous selector should not be guessed: %+v", r)
	}
	r = Normalize(model.Invocation{}, []string{"pr", "merge", "123", "--future=value"})
	if r.Operations[0].Target.Number == nil || *r.Operations[0].Target.Number != 123 {
		t.Fatalf("attached unknown flag is not ambiguous: %+v", r)
	}
}

func TestNormalizeDeepParameters(t *testing.T) {
	tests := []struct {
		argv []string
		key  string
		want any
	}{
		{[]string{"pr", "review", "1", "--approve"}, "sensitive", nil},
		{[]string{"pr", "create", "-Bmain", "-Hfeature", "-lbug", "-ldocs"}, "label", []string{"bug", "docs"}},
		{[]string{"issue", "transfer", "1", "other/repo"}, "destinationRepository", "other/repo"},
		{[]string{"repo", "rename", "new-name"}, "newName", "new-name"},
		{[]string{"repo", "clone", "o/r", "dir"}, "directory", "dir"},
		{[]string{"repo", "clone", "o/r", "--", "--depth", "1"}, "directory", nil},
		{[]string{"repo", "clone", "o/r", "dir", "--", "--depth", "1"}, "gitArguments", []string{"--depth", "1"}},
		{[]string{"release", "upload", "v1", "one.zip", "two.zip", "--clobber"}, "assets", []string{"one.zip", "two.zip"}},
		{[]string{"release", "delete-asset", "v1", "asset.zip"}, "asset", "asset.zip"},
		{[]string{"secret", "set", "NAME", "-bprivate", "-oorg"}, "body", true},
		{[]string{"variable", "get", "NAME", "--env", "production"}, "name", "NAME"},
		{[]string{"workflow", "run", "ci.yml", "-f", "name=test", "-f", "mode=fast"}, "inputs", map[string]any{"name": "test", "mode": "fast"}},
		{[]string{"run", "rerun", "42", "--failed"}, "run", "42"},
		{[]string{"label", "edit", "old", "--name", "new"}, "name", "old"},
		{[]string{"ruleset", "view", "42", "--org", "org"}, "ruleset", "42"},
		{[]string{"auth", "token", "--hostname", "ghe.example"}, "sensitive", true},
		{[]string{"auth", "login", "--with-token"}, "tokenFromStdin", true},
		{[]string{"extension", "install", "owner/gh-tool"}, "arguments", []string{"owner/gh-tool"}},
		{[]string{"alias", "set", "pm", "pr merge"}, "arguments", []string{"pm", "pr merge"}},
		{[]string{"config", "set", "editor", "vim", "--host", "ghe.example"}, "arguments", []string{"editor", "vim"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.argv, " "), func(t *testing.T) {
			r := Normalize(model.Invocation{}, tt.argv)
			if !reflect.DeepEqual(r.Operations[0].Parameters[tt.key], tt.want) {
				t.Fatalf("parameters=%v want %s=%v", r.Operations[0].Parameters, tt.key, tt.want)
			}
			if tt.argv[0] == "secret" && r.Operations[0].Target.Repository != "" {
				t.Fatalf("org secret must not target cwd repository")
			}
		})
	}
	r := Normalize(model.Invocation{}, []string{"pr", "review", "1", "--request-changes"})
	if r.Operations[0].Action != "pr.review.request-changes" {
		t.Fatalf("review action=%s", r.Operations[0].Action)
	}
}

func TestRegistryClassifiesEveryCommand(t *testing.T) {
	for path, c := range registry {
		if c.family == "" || c.action == "" || c.effect == "" {
			t.Errorf("unclassified %q", path)
		}
	}
}

func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"pr\x00merge\x00123\x00--squash", "-R\x00o/r\x00pr\x00merge\x001", "pr\x00merge\x00--future\x00123", "auth\x00login\x00-h\x00ghe.example", "", "--", "pr\x00merge\x00--body"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		argv := strings.Split(s, "\x00")
		r := Normalize(model.Invocation{}, argv)
		if len(r.Operations) == 0 {
			t.Fatal("no operations")
		}
		for _, op := range r.Operations {
			if (op.Effect == model.EffectUnknown || op.UnknownReason != model.UnknownNone) && r.Classification.MutationFree {
				t.Fatalf("unknown reported safe: %+v", r)
			}
		}
	})
}

var benchmarkResult model.Result

func BenchmarkNormalizePrMerge(b *testing.B) {
	inv := model.Invocation{Env: map[string]string{"GH_REPO": "org/repo"}}
	argv := []string{"pr", "merge", "123", "--squash", "--auto", "-d"}
	b.ReportAllocs()
	for range b.N {
		benchmarkResult = Normalize(inv, argv)
	}
}
