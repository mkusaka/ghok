package rest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
)

func classify(t *testing.T, inv model.Invocation, args ...string) model.Result {
	t.Helper()
	req, err := apiargs.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	return Normalize(inv, req)
}

func knownInv() model.Invocation {
	return model.Invocation{Env: map[string]string{"GH_HOST": "github.com"}}
}

func requireOperation(t *testing.T, res model.Result, family, action string, effect model.Effect) model.Operation {
	t.Helper()
	if len(res.Operations) != 1 {
		t.Fatalf("operations: %#v", res)
	}
	op := res.Operations[0]
	if op.Family != family || op.Action != action || op.Effect != effect {
		t.Fatalf("got %s / %s / %s; want %s / %s / %s (%+v)", op.Family, op.Action, op.Effect, family, action, effect, op)
	}
	if res.Classification.MutationFree != (effect == model.EffectRead) {
		t.Fatalf("incorrect mutation-free: %+v", res)
	}
	return op
}

func TestDirectMergeCanonical(t *testing.T) {
	for _, inv := range []model.Invocation{knownInv(), {}} {
		res := classify(t, inv, "-X", "PUT", "repos/o/r/pulls/123/merge", "-f", "merge_method=squash")
		op := requireOperation(t, res, "pr.merge", "pr.merge.direct", model.EffectWrite)
		if op.Target.Repository != "o/r" || op.Target.Owner != "o" || op.Target.Number == nil || *op.Target.Number != 123 || op.Parameters["method"] != "squash" {
			t.Fatalf("canonical merge: %+v", op)
		}
		if inv.Env == nil {
			if op.Target.Host != "" || res.Classification.Resolution != model.ResolutionDeferred || !unresolvedField(op, "target.host", "git/config") {
				t.Fatalf("default host must be deferred: %+v", res)
			}
		} else if op.Target.Host != "github.com" || res.Classification.Resolution != model.ResolutionComplete {
			t.Fatalf("explicit host: %+v", res)
		}
	}
}

func TestMethodInferenceAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		method string
		effect model.Effect
	}{
		{[]string{"mystery"}, "GET", model.EffectRead},
		{[]string{"mystery", "-f", "x=1"}, "POST", model.EffectWrite},
		{[]string{"mystery", "-F", "x=1"}, "POST", model.EffectWrite},
		{[]string{"mystery", "--input", "-"}, "POST", model.EffectWrite},
		{[]string{"mystery", "-X", "get", "-f", "x=1"}, "GET", model.EffectRead},
		{[]string{"mystery", "-XHEAD"}, "HEAD", model.EffectRead},
		{[]string{"mystery", "-XPUT"}, "PUT", model.EffectWrite},
		{[]string{"mystery", "-XPATCH"}, "PATCH", model.EffectWrite},
		{[]string{"mystery", "-XDELETE"}, "DELETE", model.EffectWrite},
		{[]string{"mystery", "-XOPTIONS"}, "OPTIONS", model.EffectWrite},
		{[]string{"mystery", "-XTRACE"}, "TRACE", model.EffectWrite},
		{[]string{"mystery", "-XCONNECT"}, "CONNECT", model.EffectWrite},
		{[]string{"mystery", "-XBANANA"}, "BANANA", model.EffectUnknown},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			res := classify(t, knownInv(), tc.args...)
			family, action := model.FamilyUnknown, "api."+strings.ToLower(tc.method)+".unknown"
			if tc.effect == model.EffectRead {
				family, action = "api.read", "api.read.unknown"
			}
			op := requireOperation(t, res, family, action, tc.effect)
			if op.Origin.Method != tc.method {
				t.Fatal(op.Origin)
			}
			if tc.effect != model.EffectRead && op.UnknownReason != model.UnknownEndpoint {
				t.Fatal(op.UnknownReason)
			}
		})
	}
}

func TestTypedRoutes(t *testing.T) {
	for _, tc := range []struct{ method, path, family, action string }{
		{"GET", "repos/o/r/pulls/1/merge", "pr.read", "pr.merge.status"},
		{"POST", "repos/o/r/pulls", "pr.create", "pr.create"},
		{"POST", "repos/o/r/pulls/1/reviews/2/events", "pr.review", "pr.review.submit"},
		{"PUT", "repos/o/r/pulls/1/reviews/2/dismissals", "pr.review", "pr.review.dismiss"},
		{"POST", "repos/o/r/pulls/1/requested_reviewers", "pr.reviewers", "pr.reviewers.request"},
		{"DELETE", "repos/o/r/pulls/1/requested_reviewers", "pr.reviewers", "pr.reviewers.remove"},
		{"PUT", "repos/o/r/pulls/1/update-branch", "pr.update-branch", "pr.update-branch"},
		{"GET", "repos/o/r/pulls/comments", "pr.read", "pr.comments.list"},
		{"PATCH", "repos/o/r/pulls/comments/1", "pr.comment", "pr.comment.update"},
		{"POST", "repos/o/r/pulls/1/comments/2/replies", "pr.comment", "pr.comment.reply"},
		{"POST", "repos/o/r/issues", "issue.create", "issue.create"},
		{"PUT", "repos/o/r/issues/1/lock", "issue.lock", "issue.lock"},
		{"DELETE", "repos/o/r/issues/1/lock", "issue.unlock", "issue.unlock"},
		{"PATCH", "repos/o/r/issues/comments/1", "issue.comment", "issue.comment.update"},
		{"DELETE", "repos/o/r/issues/comments/1", "issue.comment", "issue.comment.delete"},
		{"PUT", "repos/o/r/issues/1/labels", "issue.labels", "issue.labels.replace"},
		{"DELETE", "repos/o/r/issues/1/labels/bug", "issue.labels", "issue.labels.remove"},
		{"POST", "repos/o/r/issues/1/assignees", "issue.assignees", "issue.assignees.add"},
		{"DELETE", "repos/o/r", "repo.delete", "repo.delete"},
		{"POST", "user/repos", "repo.create", "repo.create"},
		{"POST", "orgs/o/repos", "repo.create", "repo.create"},
		{"POST", "repos/o/r/transfer", "repo.transfer", "repo.transfer"},
		{"POST", "repos/o/r/forks", "repo.fork", "repo.fork"},
		{"PUT", "repos/o/r/collaborators/u", "repo.collaborators", "repo.collaborators.add"},
		{"POST", "repos/o/r/git/refs", "ref.create", "ref.create"},
		{"PATCH", "repos/o/r/git/refs/heads/main", "ref.update", "ref.update"},
		{"DELETE", "repos/o/r/git/refs/heads/a/b", "ref.delete", "ref.delete"},
		{"PUT", "repos/o/r/branches/main/protection", "branch.protection", "branch.protection.update"},
		{"DELETE", "repos/o/r/branches/main/protection", "branch.protection", "branch.protection.delete"},
		{"POST", "repos/o/r/rulesets", "repo.rulesets", "repo.rulesets.create"},
		{"DELETE", "orgs/o/rulesets/1", "org.rulesets", "org.rulesets.delete"},
		{"PUT", "repos/o/r/contents/a%2Fb", "file.put", "file.put"},
		{"DELETE", "repos/o/r/contents/a/b", "file.delete", "file.delete"},
		{"GET", "repos/o/r/releases/latest", "release.read", "release.latest"},
		{"PATCH", "repos/o/r/releases/1", "release.update", "release.update"},
		{"POST", "repos/o/r/releases/1/assets", "release.asset", "release.asset.upload"},
		{"DELETE", "repos/o/r/releases/assets/1", "release.asset", "release.asset.delete"},
		{"POST", "repos/o/r/actions/workflows/build.yml/dispatches", "workflow.dispatch", "workflow.dispatch"},
		{"POST", "repos/o/r/actions/workflows/123/dispatches", "workflow.dispatch", "workflow.dispatch"},
		{"POST", "repos/o/r/actions/runs/1/rerun", "run.rerun", "run.rerun"},
		{"POST", "repos/o/r/actions/runs/1/cancel", "run.cancel", "run.cancel"},
		{"DELETE", "repos/o/r/actions/runs/1", "run.delete", "run.delete"},
		{"PUT", "repos/o/r/actions/secrets/KEY", "secret.put", "secret.put"},
		{"DELETE", "orgs/o/actions/secrets/KEY", "secret.delete", "secret.delete"},
		{"PUT", "repos/o/r/environments/prod/secrets/KEY", "secret.put", "secret.put"},
		{"POST", "repos/o/r/actions/variables", "variable.create", "variable.create"},
		{"PATCH", "orgs/o/actions/variables/KEY", "variable.update", "variable.update"},
		{"DELETE", "repos/o/r/environments/prod/variables/KEY", "variable.delete", "variable.delete"},
		{"POST", "repos/o/r/hooks", "hook.create", "hook.create"},
		{"DELETE", "orgs/o/hooks/1", "hook.delete", "hook.delete"},
		{"POST", "repos/o/r/keys", "key.create", "repo.keys.create"},
		{"DELETE", "user/keys/1", "key.delete", "user.keys.delete"},
		{"PATCH", "gists/abcd1234", "gist.update", "gist.update"},
		{"POST", "gists/abcd1234/comments", "gist.comment", "gist.comment.create"},
		{"DELETE", "orgs/o/members/u", "org.members", "org.members.remove"},
		{"PUT", "orgs/o/teams/t/memberships/u", "team.members", "team.membership.put"},
		{"DELETE", "orgs/o/teams/t/repos/o/r", "team.repositories", "team.repository.delete"},
		{"POST", "repos/o/r/merges", "branch.merge", "branch.merge"},
		{"POST", "repos/o/r/check-runs", "check.create", "check.create"},
		{"POST", "repos/o/r/statuses/abc123", "status.create", "status.create"},
		{"POST", "repos/o/r/deployments", "deployment.create", "deployment.create"},
		{"POST", "repos/o/r/deployments/1/statuses", "deployment.status", "deployment.status.create"},
		{"PUT", "repos/o/r/environments/prod", "environment.put", "environment.put"},
		{"PATCH", "user", "user.update", "user.update"},
		{"PUT", "notifications", "notification.mark_read", "notification.mark_read"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			res := classify(t, knownInv(), "-X", tc.method, tc.path)
			requireOperation(t, res, tc.family, tc.action, methodEffect(tc.method))
			if res.Classification.Confidence != model.ConfidenceExact {
				t.Fatal(res.Classification)
			}
		})
	}
}

func TestRouteFalsePositives(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{"PUT", "repos/o/r/pulls/123/merge-extra"},
		{"PUT", "repos/o/r/pulls/abc/merge"},
		{"PUT", "repos/o/r/pulls/-1/merge"},
		{"PUT", "repos/o/r/pulls/0/merge"},
		{"PUT", "repos/o/r/pulls/9999999999999999999999999999999/merge"},
		{"POST", "repos/merge/merge/pulls/1"},
		{"PUT", "repos/o/r/merges"},
		{"POST", "repos"},
		{"POST", "user/repos/extra"},
		{"POST", "repos/o/r/releases/latest"},
		{"POST", "repos/o/r/actions/workflows/abc/dispatches"},
		{"PUT", "repos/o%2Fx/r/pulls/1/merge"},
		{"PUT", "api/v3/repos/o/r/pulls/1/merge"},
	} {
		res := classify(t, knownInv(), "-X", tc.method, tc.path)
		op := requireOperation(t, res, model.FamilyUnknown, "api."+strings.ToLower(tc.method)+".unknown", model.EffectWrite)
		if op.UnknownReason != model.UnknownEndpoint {
			t.Fatalf("%s: %+v", tc.path, op)
		}
	}
	// Merely naming the owner and repository "merge" cannot imply a merge.
	requireOperation(t, classify(t, knownInv(), "repos/merge/merge/pulls/1"), "pr.read", "pr.view", model.EffectRead)
}

func TestStateAndReviewSpecialization(t *testing.T) {
	for _, tc := range []struct {
		method, path, key, value, family, action string
		typed                                    bool
	}{
		{"PATCH", "repos/o/r/pulls/1", "state", "closed", "pr.close", "pr.close", false},
		{"PATCH", "repos/o/r/pulls/1", "state", "open", "pr.reopen", "pr.reopen", false},
		{"PATCH", "repos/o/r/issues/1", "state", "closed", "issue.close", "issue.close", false},
		{"PATCH", "repos/o/r/issues/1", "state", "open", "issue.reopen", "issue.reopen", false},
		{"PATCH", "repos/o/r", "archived", "true", "repo.archive", "repo.archive", true},
		{"PATCH", "repos/o/r", "archived", "true", "repo.edit", "repo.edit", false},
		{"POST", "repos/o/r/pulls/1/reviews", "event", "APPROVE", "pr.review", "pr.review.approve", false},
		{"POST", "repos/o/r/pulls/1/reviews", "event", "REQUEST_CHANGES", "pr.review", "pr.review.request-changes", false},
		{"POST", "repos/o/r/pulls/1/reviews", "event", "COMMENT", "pr.review", "pr.review.comment", false},
		{"POST", "repos/o/r/pulls/1/reviews", "body", "draft", "pr.review", "pr.review.pending", false},
		{"POST", "repos/o/r/pulls/1/reviews/2/events", "event", "APPROVE", "pr.review", "pr.review.approve", false},
	} {
		flag := "-f"
		if tc.typed {
			flag = "-F"
		}
		res := classify(t, knownInv(), "-X", tc.method, tc.path, flag, tc.key+"="+tc.value)
		requireOperation(t, res, tc.family, tc.action, model.EffectWrite)
	}
	res := classify(t, knownInv(), "-XPATCH", "repos/o/r/pulls/1?state=closed")
	requireOperation(t, res, "pr.edit", "pr.edit", model.EffectWrite)
	res = classify(t, knownInv(), "-XPOST", "repos/o/r/pulls/1/reviews", "-f", "event=INVALID")
	op := requireOperation(t, res, "pr.review", "pr.review.create", model.EffectWrite)
	if !unresolvedField(op, "parameters.event", "input") {
		t.Fatal(op)
	}
}

func TestFieldsTypesNestedAndFiles(t *testing.T) {
	inv := knownInv()
	inv.ReadFile = func(path string) ([]byte, error) {
		if path != "value.txt" {
			return nil, errors.New("missing")
		}
		return []byte("literal {owner}\n"), nil
	}
	op := classify(t, inv, "gists", "-f", "raw=true", "-F", "bool=true", "-F", "false=false", "-F", "null=null", "-F", "number=42", "-F", "files[a][content]=@value.txt", "-F", "items[][name]=first", "-F", "items[][name]=second", "-F", "empty[]").Operations[0]
	want := map[string]any{
		"raw": "true", "bool": true, "false": false, "null": nil, "number": 42,
		"files": map[string]any{"a": map[string]any{"content": "literal {owner}\n"}},
		"items": []any{map[string]any{"name": "first"}, map[string]any{"name": "second"}}, "empty": []any{},
	}
	if !reflect.DeepEqual(op.Parameters, want) {
		t.Fatalf("params: %#v", op.Parameters)
	}
	inv.Stdin = model.StdinData{Status: model.StdinCaptured, Data: []byte("bytes\x00\n")}
	op = classify(t, inv, "gists", "-F", "a=@-", "-F", "b=@-").Operations[0]
	if op.Parameters["a"] != "bytes\x00\n" || op.Parameters["b"] != "" {
		t.Fatal(op.Parameters)
	}
	inv.Stdin = model.StdinData{}
	op = classify(t, inv, "gists", "-F", "a=@-", "-F", "b=@missing").Operations[0]
	if !unresolvedField(op, "parameters.a", "stdin") || !unresolvedField(op, "parameters.b", "file") {
		t.Fatal(op)
	}
}

func TestInputBodies(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		data        []byte
		status      model.StdinStatus
		unreadable  bool
		action      string
		unresolved  bool
	}{
		{"file", "body.json", []byte(`{"state":"closed","nested":{"a":[1,true]}}`), "", false, "pr.close", false},
		{"stdin", "-", []byte(`{"state":"open"}`), model.StdinCaptured, false, "pr.reopen", false},
		{"uncaptured", "-", nil, model.StdinNotCaptured, false, "pr.edit", true},
		{"tty", "-", nil, model.StdinTTY, false, "pr.edit", true},
		{"invalid", "body.json", []byte(`not json`), "", false, "pr.edit", true},
		{"array", "body.json", []byte(`[1,2]`), "", false, "pr.edit", true},
		{"null", "body.json", []byte(`null`), "", false, "pr.edit", true},
		{"trailing", "body.json", []byte(`{} {}`), "", false, "pr.edit", true},
		{"unreadable", "body.json", nil, "", true, "pr.edit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := knownInv()
			inv.Stdin = model.StdinData{Status: tc.status, Data: tc.data}
			inv.ReadFile = func(string) ([]byte, error) {
				if tc.unreadable {
					return nil, errors.New("unreadable")
				}
				return tc.data, nil
			}
			res := classify(t, inv, "-XPATCH", "repos/o/r/pulls/1", "--input", tc.input, "-f", "state=closed", "-f", "query=kept")
			op := requireOperation(t, res, tc.action, tc.action, model.EffectWrite)
			if op.Parameters["query"] != "kept" {
				t.Fatal(op.Parameters)
			}
			if unresolvedField(op, "parameters", "") != tc.unresolved {
				t.Fatalf("unresolved: %+v", op)
			}
			if tc.name == "stdin" && op.Parameters["state"] != "open" {
				t.Fatal("query overrode body", op)
			}
		})
	}
	inv := knownInv()
	inv.ReadFile = func(string) ([]byte, error) { return []byte(`{"merge_method":"rebase"}`), nil }
	op := classify(t, inv, "-XPUT", "repos/o/r/pulls/1/merge", "--input", "body.json").Operations[0]
	if op.Parameters["method"] != "rebase" {
		t.Fatal(op.Parameters)
	}
	inv.Stdin = model.StdinData{Status: model.StdinCaptured, Data: []byte(`{"event":"APPROVE"}`)}
	op = classify(t, inv, "repos/o/r/pulls/1/reviews", "--input", "-", "-F", "consumes=@-").Operations[0]
	if op.Action == "pr.review.pending" || op.Action == "pr.review.approve" || !unresolvedField(op, "parameters", "stdin") {
		t.Fatal(op)
	}
}

func TestPlaceholders(t *testing.T) {
	for _, ghRepo := range []string{"o/r", "enterprise.example/o/r", "o/123"} {
		inv := knownInv()
		inv.Env["GH_REPO"] = ghRepo
		res := classify(t, inv, "repos/{owner}/{repo}/issues/1/comments", "-F", "author={owner}", "-F", "nested[name]={repo}", "-f", "raw={owner}")
		op := requireOperation(t, res, "issue.comment", "issue.comment.create", model.EffectWrite)
		parts := strings.Split(ghRepo, "/")
		repo := parts[len(parts)-1]
		if op.Target.Repository != "o/"+repo || op.Parameters["author"] != "o" || op.Parameters["raw"] != "{owner}" || op.Parameters["nested"].(map[string]any)["name"] != repo {
			t.Fatal(op)
		}
		if op.Target.Host != "github.com" {
			t.Fatal("GH_REPO must not override request host", op)
		}
	}
	res := classify(t, model.Invocation{}, "repos/{owner}/{repo}/issues/1/comments", "-F", "author={owner}")
	op := requireOperation(t, res, "issue.comment", "issue.comment.create", model.EffectWrite)
	if op.Target.Repository != "" || op.Target.Owner != "" || !unresolvedField(op, "target.repository", "git") || !unresolvedField(op, "parameters.author", "git") || res.Classification.Resolution != model.ResolutionDeferred {
		t.Fatal(res)
	}
	inv := knownInv()
	inv.Env["GH_REPO"] = "o/r"
	op = classify(t, inv, "-XPUT", "repos/{owner}/{repo}/branches/{branch}/protection").Operations[0]
	if op.Target.Branch != "" || !unresolvedField(op, "target.branch", "git") {
		t.Fatal(op)
	}
	op = classify(t, inv, "-XPATCH", "repos/o/r/pulls/1", "-F", "head={branch}").Operations[0]
	if !unresolvedField(op, "parameters.head", "git") || op.Target.HeadBranch != "" {
		t.Fatal(op)
	}
	op = classify(t, inv, "repos/:owner/:repo/pulls/1").Operations[0]
	if op.Target.Repository != "o/r" {
		t.Fatal(op)
	}
}

func TestURLsQueriesAndHosts(t *testing.T) {
	for _, tc := range []struct{ endpoint, hostname, envHost, host string }{
		{"repos/o/r", "", "github.com", "github.com"},
		{"/repos/o/r", "enterprise.example", "ignored.example", "enterprise.example"},
		{"repos/o/r", "", "enterprise.example", "enterprise.example"},
		{"https://api.github.com/repos/o/r", "ignored.example", "ignored.example", "github.com"},
		{"https://enterprise.example/api/v3/repos/o/r", "", "", "enterprise.example"},
		{"https://api.sub.ghe.com/repos/o/r", "", "", "sub.ghe.com"},
	} {
		inv := model.Invocation{Env: map[string]string{"GH_HOST": tc.envHost}}
		args := []string{tc.endpoint}
		if tc.hostname != "" {
			args = append(args, "--hostname", tc.hostname)
		}
		res := classify(t, inv, args...)
		op := requireOperation(t, res, "repo.read", "repo.view", model.EffectRead)
		if op.Target.Host != tc.host || res.Source.Path != "/repos/o/r" {
			t.Fatal(res)
		}
	}
	res := classify(t, knownInv(), "-XGET", "repos/o/r/issues?labels=good%20first+issue&labels=bug&ref=a%2Fb", "-F", "page=2", "-F", "nested[flag]=true", "-f", "names[]=a", "-f", "names[]=b")
	op := requireOperation(t, res, "issue.read", "issue.list", model.EffectRead)
	want := map[string]any{"labels": []string{"good first issue", "bug"}, "ref": "a/b", "page": "2", "flag": "true", "names[]": []string{"a", "b"}}
	if !reflect.DeepEqual(op.Parameters, want) {
		t.Fatalf("query params: %#v", op.Parameters)
	}
	op = classify(t, knownInv(), "repos/o/r/contents/a%2Fb").Operations[0]
	if op.Parameters["path"] != "a/b" {
		t.Fatal(op)
	}
	op = classify(t, knownInv(), "-XDELETE", "repos/o/r/git/refs/heads%2Ffeature%2Fa").Operations[0]
	if op.Target.Ref != "heads/feature/a" {
		t.Fatal(op)
	}
	op = classify(t, knownInv(), "-XPUT", "repos/o/r/branches/feature%2Fa/protection").Operations[0]
	if op.Target.Branch != "feature/a" {
		t.Fatal(op)
	}
	op = classify(t, knownInv(), "https://arbitrary.example/repos/o/r/pulls/1/merge").Operations[0]
	if op.Effect != model.EffectUnknown || op.Family != model.FamilyUnknown {
		t.Fatal(op)
	}
}

func TestTargetExtraction(t *testing.T) {
	inv := knownInv()
	op := classify(t, inv, "-XPATCH", "repos/o/r/git/refs/heads/main", "-F", "force=true", "-f", "sha=abc").Operations[0]
	if op.Target.Ref != "heads/main" || op.Target.SHA != "abc" || op.Parameters["force"] != true {
		t.Fatal(op)
	}
	op = classify(t, inv, "repos/o/r/git/refs", "-f", "ref=refs/heads/main", "-f", "sha=abc").Operations[0]
	if op.Target.Ref != "refs/heads/main" {
		t.Fatal(op)
	}
	op = classify(t, inv, "repos/o/r/pulls", "-f", "base=main", "-f", "head=feature").Operations[0]
	if op.Target.BaseBranch != "main" || op.Target.HeadBranch != "feature" {
		t.Fatal(op)
	}
	op = classify(t, inv, "orgs/o/repos", "-f", "name=new").Operations[0]
	if op.Target.Repository != "o/new" {
		t.Fatal(op)
	}
	op = classify(t, inv, "user/repos", "-f", "name=new").Operations[0]
	if !unresolvedField(op, "target.owner", "api") || op.Target.Repository != "" {
		t.Fatal(op)
	}
	op = classify(t, inv, "repos/o/r/statuses/abc123", "-f", "state=success").Operations[0]
	if op.Target.SHA != "abc123" {
		t.Fatal(op)
	}
}

func TestMalformedAndHeaders(t *testing.T) {
	for _, path := range []string{"", "repos/o/r/", "//repos/o/r", "repos//r", "repos/o/../r", "repos/o/%2e%2e/r", "repos/o/r/contents/a%2F..%2Fb", "repos/o/r%", "repos/o/r?x=%", "https://", "ftp://api.github.com/repos/o/r", "https://user:" + "pass@api.github.com/repos/o/r", "repos/o/r#fragment", "repos/o/r\n", "repos\\o\\r"} {
		res := Normalize(knownInv(), model.APIRequest{Method: "GET", Endpoint: path})
		if res.Classification.MutationFree || res.Operations[0].UnknownReason != model.UnknownMalformed {
			t.Fatalf("%q: %+v", path, res)
		}
	}
	for _, method := range []string{"GET", "POST", "PUT"} {
		res := classify(t, knownInv(), "-X", method, "repos/o/r", "-H", "x-http-method-override: DELETE", "-H", "Authorization: secret")
		op := requireOperation(t, res, model.FamilyUnknown, "api."+strings.ToLower(method)+".unknown", model.EffectUnknown)
		if op.UnknownReason != model.UnknownOperationAmbig || !reflect.DeepEqual(op.Parameters["headers"], []string{"X-HTTP-Method-Override"}) {
			t.Fatal(op)
		}
		if strings.Contains(op.Detail, "DELETE") || strings.Contains(op.Detail, "secret") {
			t.Fatal("header value leaked")
		}
	}
	res := classify(t, knownInv(), "repos/o/r", "-H", "Accept: application/vnd.github.preview+json")
	op := requireOperation(t, res, "repo.read", "repo.view", model.EffectRead)
	if _, ok := op.Parameters["headers"]; ok {
		t.Fatal(op)
	}
	for _, args := range [][]string{
		{"repos/o/r", "-H", "bad"},
		{"repos/o/r", "-f", "key"},
		{"repos/o/r", "-f", "key=x", "-f", "key=y"},
		{"repos/o/r", "-XPOST", "--paginate"},
		{"repos/o/r", "--paginate", "--input", "-"},
	} {
		res := classify(t, knownInv(), args...)
		if res.Classification.MutationFree || res.Operations[0].Execution != model.ExecutionSkipped || res.Operations[0].UnknownReason != model.UnknownMalformed {
			t.Fatalf("%v: %+v", args, res)
		}
	}
	res = classify(t, knownInv(), "repos/o/r/issues", "--paginate", "-f", "title=new")
	op = requireOperation(t, res, "issue.create", "issue.create", model.EffectWrite)
	if op.Execution != model.ExecutionWillRun || op.Detail == "" {
		t.Fatal(op)
	}
}

func TestMalformedFieldsNeverPanic(t *testing.T) {
	for _, key := range []string{"a]][", "][", "[", "", "x[y][", "😀]]["} {
		res := Normalize(knownInv(), model.APIRequest{Endpoint: "gists", Method: "POST", Fields: []model.APIField{{Key: key, Value: "x"}}})
		if res.Classification.MutationFree || len(res.Operations) != 1 {
			t.Fatalf("%q: %+v", key, res)
		}
	}
	inv := knownInv()
	inv.ReadFile = func(string) ([]byte, error) { panic("reader error") }
	res := classify(t, inv, "gists", "--input", "panic")
	if res.Classification.MutationFree || res.Operations[0].UnknownReason != model.UnknownParseError {
		t.Fatal(res)
	}
}

func unresolvedField(op model.Operation, field, source string) bool {
	for _, u := range op.Unresolved {
		if u.Field == field && (source == "" || u.Source == source) {
			return true
		}
	}
	return false
}

func FuzzNormalizeREST(f *testing.F) {
	for _, seed := range []struct{ path, method, key, value string }{
		{"repos/o/r/pulls/1/merge", "PUT", "merge_method", "squash"},
		{"repos/{owner}/{repo}/issues", "POST", "title", "{branch}"},
		{"https://enterprise.example/api/v3/repos/o/r", "GET", "x[y]", "true"},
		{"repos/o/r/contents/a%2Fb?ref=main", "DELETE", "a[]", "@-"},
		{"gists", "POST", "a]][", "x"},
		{"mystery", "BANANA", "", ""},
	} {
		f.Add(seed.path, seed.method, seed.key, seed.value, true)
	}
	f.Fuzz(func(t *testing.T, path, method, key, value string, typed bool) {
		res := Normalize(model.Invocation{}, model.APIRequest{Endpoint: path, Method: method, Fields: []model.APIField{{Key: key, Value: value, Typed: typed}}})
		if len(res.Operations) != 1 {
			t.Fatal(res)
		}
		op := res.Operations[0]
		if res.Classification.MutationFree && (op.Effect != model.EffectRead || (strings.ToUpper(method) != "GET" && strings.ToUpper(method) != "HEAD") || op.Family == model.FamilyUnknown) {
			t.Fatal("unsafe mutation-free classification", res)
		}
		if op.Family == model.FamilyUnknown && op.UnknownReason == model.UnknownNone {
			t.Fatal("missing unknown reason", res)
		}
	})
}

func BenchmarkRouteMatching(b *testing.B) {
	for _, tc := range []struct {
		method   string
		segments []string
	}{
		{"PUT", []string{"repos", "o", "r", "pulls", "123", "merge"}},
		{"GET", []string{"repos", "o", "r", "pulls", "comments", "12"}},
		{"POST", []string{"repos", "o", "r", "merges"}},
		{"PATCH", []string{"not", "registered"}},
	} {
		b.Run(tc.method+"/"+strings.Join(tc.segments, "/"), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				matchRoute(tc.method, tc.segments)
			}
		})
	}
}

func TestUncertainParametersAndTargets(t *testing.T) {
	res := classify(t, knownInv(), "repos/o/r/pulls/1/reviews", "-F", "event=null")
	op := res.Operations[0]
	if op.Action == "pr.review.pending" || !unresolvedField(op, "parameters.event", "input") {
		t.Fatalf("explicit null event is not an absent event: %+v", op)
	}
	res = classify(t, knownInv(), "-XPUT", "repos/o/r/pulls/1/merge", "-F", "merge_method=@missing")
	if !unresolvedField(res.Operations[0], "parameters.method", "file") {
		t.Fatalf("merge method uncertainty must use the canonical name: %+v", res)
	}
	res = classify(t, knownInv(), "-XPUT", "repos/o%2Fx/r/pulls/1/merge")
	if res.Operations[0].Target.Repository != "" {
		t.Fatalf("ambiguous repository target: %+v", res)
	}
}
