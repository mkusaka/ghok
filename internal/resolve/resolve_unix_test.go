//go:build unix

package resolve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/model"
)

func fakeGH(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "gh")
	marker := filepath.Join(dir, "calls")
	put(t, exe, "#!/bin/sh\nset -eu\n[ \"$GHOK_NESTED\" = 1 ] || exit 91\nprintf 'call\\n' >> \"$MARKER\"\n"+body+"\n")
	if err := os.Chmod(exe, 0700); err != nil {
		t.Fatal(err)
	}
	return exe, marker
}

func prResult() model.Result {
	return model.Result{Classification: model.Classification{Level: model.LevelL1, Resolution: model.ResolutionDeferred}, Operations: []model.Operation{{Family: "pr.merge", Action: "pr.merge.request", Effect: model.EffectWrite, Origin: model.Origin{Command: []string{"pr", "merge"}}, Unresolved: []model.Unresolved{{Field: "target.repository", Reason: "deferred", Source: "git"}, {Field: "target.host", Reason: "deferred", Source: "git"}, {Field: "target.number", Reason: "current branch PR", Source: "api"}}}}}
}

func nodeOperation(id string, fields ...string) model.Operation {
	op := model.Operation{Family: "pr.merge", Action: "pr.merge.direct", Effect: model.EffectWrite, Target: model.Target{NodeID: id, Host: "github.com"}}
	for _, field := range fields {
		op.Unresolved = append(op.Unresolved, model.Unresolved{Field: field, Reason: "node target", Source: "api"})
	}
	return op
}

func TestFillLocalAndNoNetwork(t *testing.T) {
	for _, tt := range []struct {
		name  string
		allow bool
		need  []string
	}{
		{"API disallowed", false, []string{"target.number"}},
		{"number not requested", true, []string{"target.repository"}},
		{"no needs", true, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exe, marker := fakeGH(t, "exit 92")
			r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
			res := prResult()
			diagnostics := r.Fill(t.Context(), &res, tt.need, tt.allow)
			target := res.Operations[0].Target
			if len(diagnostics) != 0 || target.Repository != "acme/widget" || target.Owner != "acme" || target.Host != "github.com" || target.HeadBranch != "feature" || target.Number != nil {
				t.Fatalf("result = %+v diagnostics=%v", res, diagnostics)
			}
			if res.Classification.Resolution != model.ResolutionPartial || len(res.Operations[0].Unresolved) != 1 || res.Operations[0].Unresolved[0].Field != "target.number" {
				t.Fatalf("result = %+v", res)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unexpected API call: %v", err)
			}
		})
	}
}

func TestFillPRNumber(t *testing.T) {
	exe, marker := fakeGH(t, `[ "$#" = 6 ] || exit 93
[ "$1" = api ] || exit 94
[ "$2" = 'repos/acme/widget/pulls?head=acme%3Afeature&state=open&per_page=2' ] || exit 95
[ "$3" = --hostname ] && [ "$4" = github.com ] || exit 96
[ "$5" = --method ] && [ "$6" = GET ] || exit 97
[ -z "$(/bin/cat)" ] || exit 98
printf '[{"number":42}]'`)
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker, "HOME": t.TempDir(), "PATH": "/nonexistent"}, RealGh: exe}
	res := prResult()
	diagnostics := r.Fill(t.Context(), &res, []string{"target.number"}, true)
	if len(diagnostics) != 0 || res.Operations[0].Target.Number == nil || *res.Operations[0].Target.Number != 42 || res.Classification.Resolution != model.ResolutionComplete {
		t.Fatalf("result = %+v diagnostics=%v", res, diagnostics)
	}
}

func TestFillCacheConcurrent(t *testing.T) {
	exe, marker := fakeGH(t, "printf '[{\"number\":42}]'")
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			res := prResult()
			diagnostics := r.Fill(context.Background(), &res, []string{"target.number"}, true)
			if len(diagnostics) != 0 || res.Operations[0].Target.Number == nil || *res.Operations[0].Target.Number != 42 {
				t.Errorf("result = %+v diagnostics=%v", res, diagnostics)
			}
			_ = r.Local()
		})
	}
	wg.Wait()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "call\n" {
		t.Fatalf("calls = %q", data)
	}
}

func TestFillPRLookupFailures(t *testing.T) {
	for _, tt := range []struct{ name, body, reason string }{
		{"none", `printf '[]'`, "found 0"},
		{"ambiguous", `printf '[{"number":1},{"number":2}]'`, "found 2"},
		{"bad number", `printf '[{"number":0}]'`, "invalid number"},
		{"invalid JSON", `printf 'bad'`, "invalid JSON"},
		{"gh error", `printf 'secret token' >&2; exit 7`, "status 7"},
		{"stdout limit", `/usr/bin/yes x`, "stdout exceeds 1 MiB"},
		{"stderr limit", `/usr/bin/yes x >&2`, "stderr exceeds 64 KiB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exe, marker := fakeGH(t, tt.body)
			r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe, Timeout: 5 * time.Second}
			res := prResult()
			diagnostics := r.Fill(t.Context(), &res, []string{"target.number"}, true)
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], tt.reason) || strings.Contains(diagnostics[0], "secret") || res.Operations[0].Target.Number != nil || !hasUnresolved(res.Operations[0].Unresolved, "target.number") || res.Classification.Resolution != model.ResolutionPartial {
				t.Fatalf("result = %+v diagnostics=%v", res, diagnostics)
			}
			second := prResult()
			r.Fill(t.Context(), &second, []string{"target.number"}, true)
			data, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "call\n" {
				t.Fatalf("failure not cached: %q", data)
			}
		})
	}
}

func TestFillTimeoutAndCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelParent), func(t *testing.T) {
			exe, marker := fakeGH(t, `printf '%s' "$$" > "$PIDFILE"
exec /bin/sleep 60`)
			pidfile := filepath.Join(t.TempDir(), "pid")
			r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker, "PIDFILE": pidfile}, RealGh: exe, Timeout: 2 * time.Second}
			ctx := t.Context()
			if cancelParent {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			res := prResult()
			start := time.Now()
			diagnostics := r.Fill(ctx, &res, []string{"target.number"}, true)
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "deadline exceeded") || time.Since(start) > 5*time.Second {
				t.Fatalf("diagnostics=%v elapsed=%v", diagnostics, time.Since(start))
			}
			data, err := os.ReadFile(pidfile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Fatalf("helper survived timeout: pid=%d err=%v", pid, err)
			}
		})
	}
}

func TestFillNodeLookup(t *testing.T) {
	exe, marker := fakeGH(t, `[ "$#" = 8 ] || exit 93
[ "$1" = api ] && [ "$2" = graphql ] || exit 94
[ "$3" = --hostname ] && [ "$4" = github.com ] || exit 95
[ "$5" = -f ] && [ "$7" = -f ] && [ "$8" = 'id=PR_node' ] || exit 96
case "$6" in 'query=query($id:ID!){node(id:$id)'*) ;; *) exit 97 ;; esac
printf '{"data":{"node":{"__typename":"PullRequest","number":73,"repository":{"nameWithOwner":"different/repo"}}}}'`)
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	fields := []string{"target.repository", "target.owner", "target.number"}
	res := model.Result{Classification: model.Classification{Level: model.LevelL3}, Operations: []model.Operation{nodeOperation("PR_node", fields...)}}
	diagnostics := r.Fill(t.Context(), &res, fields, true)
	target := res.Operations[0].Target
	if len(diagnostics) != 0 || target.Repository != "different/repo" || target.Owner != "different" || target.Number == nil || *target.Number != 73 || res.Classification.Resolution != model.ResolutionComplete {
		t.Fatalf("result=%+v diagnostics=%v", res, diagnostics)
	}
}

func TestFillNodeFailures(t *testing.T) {
	for _, response := range []string{`bad`, `{"data":{"node":null}}`, `{"errors":[{"message":"secret"}]}`, `{"data":{"node":{"__typename":"User"}}}`, `{"data":{"node":{"__typename":"PullRequest","number":2,"repository":{"nameWithOwner":"invalid"}}}}`, `{"data":{"node":{"__typename":"Issue","number":2,"repository":{"nameWithOwner":"a/b"}}}}`} {
		t.Run(response, func(t *testing.T) {
			exe, marker := fakeGH(t, `printf '%s' '`+response+`'`)
			r := Resolver{Env: map[string]string{"MARKER": marker}, Cwd: t.TempDir(), RealGh: exe}
			res := model.Result{Operations: []model.Operation{nodeOperation("node", "target.repository", "target.number")}}
			diagnostics := r.Fill(t.Context(), &res, []string{"target.repository", "target.number"}, true)
			if len(diagnostics) != 1 || strings.Contains(diagnostics[0], "secret") || res.Operations[0].Target.Repository != "" || res.Operations[0].Target.Number != nil || len(res.Operations[0].Unresolved) != 2 {
				t.Fatalf("result=%+v diagnostics=%v", res, diagnostics)
			}
		})
	}
}

func TestFillUncertaintyAndExplicitTargets(t *testing.T) {
	exe, marker := fakeGH(t, "exit 99")
	config := originConfig + "[remote \"upstream\"]\nurl=https://github.com/base/widget\n"
	r := Resolver{Cwd: fixture(t, config), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	res := prResult()
	for range 2 {
		r.Fill(t.Context(), &res, []string{"target.number"}, true)
		if !hasUnresolved(res.Operations[0].Unresolved, "target.repository") || res.Classification.Resolution != model.ResolutionPartial {
			t.Fatalf("ambiguity erased: %+v", res)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("ambiguous lookup called: %v", err)
	}
	explicit := prResult()
	explicit.Operations[0].Target = model.Target{Host: "enterprise.example", Owner: "explicit", Repository: "explicit/repo", Number: model.IntPtr(4)}
	r.Fill(t.Context(), &explicit, nil, false)
	if target := explicit.Operations[0].Target; target.Repository != "explicit/repo" || target.Host != "enterprise.example" || target.Owner != "explicit" || *target.Number != 4 {
		t.Fatalf("explicit target overwritten: %+v", target)
	}
	unknown := model.NewUnknown(model.LevelL1, model.UnknownCommand, "unknown")
	r.Fill(t.Context(), &unknown, nil, false)
	if unknown.Classification.Resolution != model.ResolutionUnresolved || unknown.Classification.MutationFree {
		t.Fatalf("unknown became safe: %+v", unknown)
	}
	skipped := model.Result{Operations: []model.Operation{nodeOperation("node", "target.number")}}
	skipped.Operations[0].Execution = model.ExecutionSkipped
	r.Fill(t.Context(), &skipped, []string{"target.number"}, true)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("skipped lookup called: %v", err)
	}
}

func TestFillNoNodeLocalRepositoryAndHostOnly(t *testing.T) {
	exe, marker := fakeGH(t, "exit 99")
	for _, allow := range []bool{false, true} {
		r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
		res := model.Result{Operations: []model.Operation{nodeOperation("node", "target.repository", "target.number")}}
		r.Fill(t.Context(), &res, []string{"target.host"}, allow)
		if res.Operations[0].Target.Repository != "" || res.Operations[0].Target.Number != nil {
			t.Fatalf("node filled from cwd: %+v", res)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unrequested node lookup: %v", err)
	}
}

func TestFillMissingExecutableAndNeeds(t *testing.T) {
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{}, RealGh: "gh"}
	res := prResult()
	diagnostics := r.Fill(t.Context(), &res, []string{"target.number"}, true)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "absolute") {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	empty := model.Result{Operations: []model.Operation{{Family: "repo.read", Effect: model.EffectRead}}}
	r.Fill(t.Context(), &empty, []string{"target.number"}, false)
	if !hasUnresolved(empty.Operations[0].Unresolved, "target.number") || empty.Classification.Resolution == model.ResolutionComplete {
		t.Fatalf("missing requested field became complete: %+v", empty)
	}
}

func TestFillGraphQLVariablesStayUnresolved(t *testing.T) {
	exe, marker := fakeGH(t, "exit 99")
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	fields := []string{"target.repository", "target.number", "target.sha"}
	op := model.Operation{Family: "pr.merge", Effect: model.EffectWrite}
	for _, field := range fields {
		op.Unresolved = append(op.Unresolved, model.Unresolved{Field: field, Reason: "missing variable", Source: "variable"})
	}
	res := model.Result{Classification: model.Classification{Level: model.LevelL3}, Operations: []model.Operation{op}}
	r.Fill(t.Context(), &res, fields, true)
	target := res.Operations[0].Target
	if target.Repository != "" || target.SHA != "" || target.Number != nil || len(res.Operations[0].Unresolved) != 3 {
		t.Fatalf("GraphQL target guessed from git: %+v", res)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("lookup without a node ID: %v", err)
	}
}

func TestFillNodeRepositoryMismatch(t *testing.T) {
	exe, marker := fakeGH(t, `printf '{"data":{"node":{"__typename":"PullRequest","number":73,"repository":{"nameWithOwner":"different/repo"}}}}'`)
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	op := nodeOperation("node", "target.number")
	op.Target.Repository = "explicit/repo"
	res := model.Result{Classification: model.Classification{Level: model.LevelL3}, Operations: []model.Operation{op}}
	diagnostics := r.Fill(t.Context(), &res, []string{"target.number"}, true)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "differs") || res.Operations[0].Target.Number != nil || !hasUnresolved(res.Operations[0].Unresolved, "target.number") {
		t.Fatalf("mismatched node accepted: %+v diagnostics=%v", res, diagnostics)
	}
}

func TestFillExplicitBranchEscaping(t *testing.T) {
	exe, marker := fakeGH(t, `[ "$2" = 'repos/acme/widget/pulls?head=acme%3Atopic%2Fone%26two&state=open&per_page=2' ] || exit 95
printf '[{"number":42}]'`)
	r := Resolver{Cwd: fixture(t, originConfig), Env: map[string]string{"MARKER": marker}, RealGh: exe}
	res := prResult()
	res.Operations[0].Target.Branch = "topic/one&two"
	diagnostics := r.Fill(t.Context(), &res, []string{"target.number"}, true)
	if len(diagnostics) != 0 || res.Operations[0].Target.Number == nil || *res.Operations[0].Target.Number != 42 {
		t.Fatalf("branch query failed: %+v diagnostics=%v", res, diagnostics)
	}
}
