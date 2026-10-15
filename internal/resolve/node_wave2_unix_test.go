//go:build unix

package resolve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
)

func TestNodeSpecializesAndFillsIdentity(t *testing.T) {
	for _, tc := range []struct{ action, typename, kind, family, specialized, fields string }{
		{"comment.create", "PullRequest", "pullRequest", "pr.comment", "pr.comment.create", `"number":19,"repository":{"nameWithOwner":"node/repo"}`},
		{"comment.create", "Issue", "issue", "issue.comment", "issue.comment.create", `"number":19,"repository":{"nameWithOwner":"node/repo"}`},
		{"lockable.lock", "Issue", "issue", "issue.lock", "issue.lock", `"number":19,"repository":{"nameWithOwner":"node/repo"}`},
		{"comment.edit", "IssueComment", "comment", "comment", "comment.edit", `"repository":{"nameWithOwner":"node/repo"}`},
		{"pr.create", "Repository", "repository", "pr.create", "pr.create", `"nameWithOwner":"node/repo"`},
		{"ref.delete", "Ref", "ref", "ref", "ref.delete", `"name":"topic","repository":{"nameWithOwner":"node/repo"}`},
	} {
		t.Run(tc.action+"/"+tc.typename, func(t *testing.T) {
			exe, marker := fakeGH(t, `printf '%s' '{"data":{"node":{"__typename":"`+tc.typename+`",`+tc.fields+`}}}'`)
			r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe, Timeout: 15 * time.Second} // generous: fake gh is slow under parallel test load
			op := model.Operation{Family: tc.family, Action: tc.action, Effect: model.EffectWrite, Target: model.Target{Host: "github.com", NodeID: "opaque", Kind: "unknown"}, Unresolved: []model.Unresolved{{Field: "target.kind"}, {Field: "target.repository"}}}
			if tc.typename == "Repository" {
				op.Target.Kind = "repository"
			}
			res := model.Result{Classification: model.Classification{Level: model.LevelL3}, Operations: []model.Operation{op}}
			if diagnostics := r.Fill(t.Context(), &res, []string{"target.repository"}, true); len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			got := res.Operations[0]
			if got.Action != tc.specialized || got.Family != tc.family || got.Target.Kind != tc.kind || got.Target.Repository != "node/repo" || got.Target.Owner != "node" || got.Target.Branch != "" || got.Target.Ref != "" || len(got.Unresolved) != 0 {
				t.Fatalf("resolved operation: %+v", got)
			}
			if tc.kind == "issue" || tc.kind == "pullRequest" {
				if got.Target.Number == nil || *got.Target.Number != 19 {
					t.Fatalf("number: %+v", got.Target)
				}
			}
		})
	}
}

func TestNodeKindOnlyAndLookupFailure(t *testing.T) {
	for _, response := range []string{`{"data":{"node":{"__typename":"PullRequest","number":7,"repository":{"nameWithOwner":"org/repo"}}}}`, `bad`} {
		t.Run(response, func(t *testing.T) {
			exe, marker := fakeGH(t, `printf '%s' '`+response+`'`)
			r := Resolver{Cwd: t.TempDir(), Env: map[string]string{"MARKER": marker}, RealGh: exe, Timeout: 15 * time.Second} // generous: fake gh is slow under parallel test load
			res := normalize.Normalize(model.Invocation{Argv: []string{"api", "graphql", "-f", `query=mutation { addComment(input:{subjectId:"opaque",body:"text"}) { clientMutationId } }`}})
			diagnostics := r.Fill(t.Context(), &res, []string{"target.kind"}, true)
			op := res.Operations[0]
			if response == "bad" {
				if len(diagnostics) != 1 || op.Action != "comment.create" || op.Target.Kind != "unknown" || !hasUnresolved(op.Unresolved, "target.kind") {
					t.Fatalf("failed lookup: %+v, %v", op, diagnostics)
				}
			} else if len(diagnostics) != 0 || op.Action != "pr.comment.create" || op.Family != "pr.comment" || op.Target.Repository != "org/repo" || op.Target.Number == nil || *op.Target.Number != 7 || hasUnresolved(op.Unresolved, "target.kind") {
				t.Fatalf("kind lookup: %+v, %v", op, diagnostics)
			}
		})
	}
}

func TestNodeDiskCacheAcrossInvocations(t *testing.T) {
	exe, marker := fakeGH(t, `printf '{"data":{"node":{"__typename":"PullRequest","number":73,"repository":{"nameWithOwner":"different/repo"}}}}'`)
	env := map[string]string{"MARKER": marker, "GHOK_CONFIG_DIR": t.TempDir()}
	lookup := func() model.Result {
		r := Resolver{Cwd: t.TempDir(), Env: env, RealGh: exe, Timeout: 15 * time.Second} // generous: fake gh is slow under parallel test load
		res := model.Result{Operations: []model.Operation{nodeOperation("opaque", "target.repository")}}
		if diagnostics := r.Fill(t.Context(), &res, []string{"target.repository"}, true); len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
		return res
	}
	lookup()
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	got := lookup().Operations[0].Target
	if got.Kind != "pullRequest" || got.Repository != "different/repo" || got.Number == nil || *got.Number != 73 {
		t.Fatalf("cached identity: %+v", got)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "call\n" {
		t.Fatalf("API calls: %q, %v", data, err)
	}
	info, err := os.Stat(filepath.Join(env["GHOK_CONFIG_DIR"], "state", "nodes.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache permissions: %v, %v", info, err)
	}
}

func TestNodeCacheRejectsExpiredCorruptAndOtherHost(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		age        time.Duration
		corrupt    bool
	}{
		{"expired", "github.com", 25 * time.Hour, false},
		{"other host", "enterprise.example", time.Hour, false},
		{"future", "github.com", -time.Hour, false},
		{"corrupt", "github.com", time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, marker := fakeGH(t, `printf '{"data":{"node":{"__typename":"PullRequest","number":73,"repository":{"nameWithOwner":"fresh/repo"}}}}'`)
			dir := t.TempDir()
			entry := nodeEntry{Kind: "pullRequest", Repository: "stale/repo", Number: 1, Host: tc.host, FetchedAt: time.Now().Add(-tc.age)}
			data, err := json.Marshal(map[string]nodeEntry{"opaque": entry})
			if err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				data = []byte(`{"broken"`)
			}
			put(t, filepath.Join(dir, "state", "nodes.json"), string(data))
			r := Resolver{Cwd: t.TempDir(), Env: map[string]string{"MARKER": marker, "GHOK_CONFIG_DIR": dir}, RealGh: exe, Timeout: 15 * time.Second} // generous: fake gh is slow under parallel test load
			res := model.Result{Operations: []model.Operation{nodeOperation("opaque", "target.repository")}}
			if diagnostics := r.Fill(t.Context(), &res, []string{"target.repository"}, true); len(diagnostics) != 0 || res.Operations[0].Target.Repository != "fresh/repo" {
				t.Fatalf("cache recovery: %+v, %v", res, diagnostics)
			}
		})
	}
}

func TestNodeCacheBoundKeepsFreshest(t *testing.T) {
	r := Resolver{env: map[string]string{}, nodesLoaded: true, nodes: make(map[string]nodeEntry)}
	now := time.Now()
	for i := range nodeCacheLimit + 1 {
		r.nodes[fmt.Sprint(i)] = nodeEntry{Kind: "issue", Repository: "org/repo", Number: 1, Host: "github.com", FetchedAt: now.Add(-time.Duration(i) * time.Second)}
	}
	r.trimNodes()
	if len(r.nodes) != nodeCacheLimit {
		t.Fatalf("cache size: %d", len(r.nodes))
	}
	if _, ok := r.nodes[fmt.Sprint(nodeCacheLimit)]; ok {
		t.Fatal("oldest cache entry survived eviction")
	}
	if _, ok := r.nodes["0"]; !ok {
		t.Fatal("freshest entry evicted")
	}
}

func TestNodeDefaultTimeoutLeavesIdentityUnresolved(t *testing.T) {
	exe, marker := fakeGH(t, `exec /bin/sleep 60`)
	r := Resolver{Cwd: t.TempDir(), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	res := model.Result{Operations: []model.Operation{{Family: "comment", Action: "comment.create", Effect: model.EffectWrite, Target: model.Target{Host: "github.com", Kind: "unknown", NodeID: "opaque"}}}}
	start := time.Now()
	diagnostics := r.Fill(t.Context(), &res, []string{"target.kind", "target.repository"}, true)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "deadline exceeded") || time.Since(start) > 4*time.Second || res.Operations[0].Target.Kind != "unknown" || !hasUnresolved(res.Operations[0].Unresolved, "target.repository") {
		t.Fatalf("timeout: %+v, %v", res, diagnostics)
	}
}
