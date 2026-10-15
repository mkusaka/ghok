package normalize_test

import (
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
	"github.com/mkusaka/ghok/internal/normalize/actions"
)

func operation(t *testing.T, argv []string) model.Operation {
	t.Helper()
	result := normalize.Normalize(model.Invocation{Argv: argv, Env: map[string]string{"GH_REPO": "o/r", "GH_HOST": "github.com"}})
	if len(result.Operations) != 1 {
		t.Fatalf("operations for %q: %+v", argv, result)
	}
	return result.Operations[0]
}

func TestCanonicalActionsAcrossLayers(t *testing.T) {
	for _, tc := range []struct {
		action, native, rest, graphql string
	}{
		{"pr.create", "pr create --title title --body body", "api repos/o/r/pulls -f title=title -f base=main -f head=topic", `createPullRequest(input:{repositoryId:"R",title:"title",baseRefName:"main",headRefName:"topic"})`},
		{"pr.edit", "pr edit 1 --title title", "api -X PATCH repos/o/r/pulls/1 -f title=title", `updatePullRequest(input:{pullRequestId:"P",title:"title"})`},
		{"pr.close", "pr close 1", "api -X PATCH repos/o/r/pulls/1 -f state=closed", `closePullRequest(input:{pullRequestId:"P"})`},
		{"pr.close", "pr close 1", "api -X PATCH repos/o/r/pulls/1 -f state=closed", `updatePullRequest(input:{pullRequestId:"P",state:CLOSED})`},
		{"pr.reopen", "pr reopen 1", "api -X PATCH repos/o/r/pulls/1 -f state=open", `reopenPullRequest(input:{pullRequestId:"P"})`},
		{"pr.reopen", "pr reopen 1", "api -X PATCH repos/o/r/pulls/1 -f state=open", `updatePullRequest(input:{pullRequestId:"P",state:OPEN})`},
		{"pr.merge.direct", "pr merge 1 --admin --squash", "api -X PUT repos/o/r/pulls/1/merge -f merge_method=squash", `mergePullRequest(input:{pullRequestId:"P",mergeMethod:SQUASH})`},
		{"pr.merge.auto.enable", "pr merge 1 --auto", "", `enablePullRequestAutoMerge(input:{pullRequestId:"P"})`},
		{"pr.merge.auto.disable", "pr merge 1 --disable-auto", "", `disablePullRequestAutoMerge(input:{pullRequestId:"P"})`},
		{"pr.review.approve", "pr review 1 --approve", "api repos/o/r/pulls/1/reviews -f event=APPROVE", `addPullRequestReview(input:{pullRequestId:"P",event:APPROVE})`},
		{"pr.review.request-changes", "pr review 1 --request-changes -b body", "api repos/o/r/pulls/1/reviews -f event=REQUEST_CHANGES", `addPullRequestReview(input:{pullRequestId:"P",event:REQUEST_CHANGES})`},
		{"pr.review.comment", "pr review 1 --comment -b body", "api repos/o/r/pulls/1/reviews -f event=COMMENT", `addPullRequestReview(input:{pullRequestId:"P",event:COMMENT})`},
		{"pr.review.approve", "pr review 1 --approve", "api repos/o/r/pulls/1/reviews/2/events -f event=APPROVE", `submitPullRequestReview(input:{pullRequestReviewId:"RV",event:APPROVE})`},
		{"pr.review.request-changes", "pr review 1 --request-changes -b body", "api repos/o/r/pulls/1/reviews/2/events -f event=REQUEST_CHANGES", `submitPullRequestReview(input:{pullRequestReviewId:"RV",event:REQUEST_CHANGES})`},
		{"pr.review.comment", "pr review 1 --comment -b body", "api repos/o/r/pulls/1/reviews/2/events -f event=COMMENT", `submitPullRequestReview(input:{pullRequestReviewId:"RV",event:COMMENT})`},
		{"issue.create", "issue create --title title --body body", "api repos/o/r/issues -f title=title", `createIssue(input:{repositoryId:"R",title:"title"})`},
		{"issue.edit", "issue edit 1 --title title", "api -X PATCH repos/o/r/issues/1 -f title=title", `updateIssue(input:{id:"I",title:"title"})`},
		{"issue.close", "issue close 1", "api -X PATCH repos/o/r/issues/1 -f state=closed", `closeIssue(input:{issueId:"I"})`},
		{"issue.close", "issue close 1", "api -X PATCH repos/o/r/issues/1 -f state=closed", `updateIssue(input:{id:"I",state:CLOSED})`},
		{"issue.reopen", "issue reopen 1", "api -X PATCH repos/o/r/issues/1 -f state=open", `reopenIssue(input:{issueId:"I"})`},
		{"issue.reopen", "issue reopen 1", "api -X PATCH repos/o/r/issues/1 -f state=open", `updateIssue(input:{id:"I",state:OPEN})`},
		{"release.edit", "release edit v1 --title title", "api -X PATCH repos/o/r/releases/1 -f name=title", ""},
		{"gist.edit", "gist edit abc --filename file", "api -X PATCH gists/abc -f description=title", ""},
	} {
		t.Run(tc.action+"/"+tc.graphql, func(t *testing.T) {
			inputs := [][]string{strings.Fields(tc.native)}
			if tc.rest != "" {
				inputs = append(inputs, strings.Fields(tc.rest))
			}
			if tc.graphql != "" {
				inputs = append(inputs, []string{"api", "graphql", "-f", "query=mutation { " + tc.graphql + " { clientMutationId } }"})
			}
			for _, argv := range inputs {
				op := operation(t, argv)
				want := actions.Must(tc.action)
				if op.Action != want.Action || op.Family != want.Family || op.Effect != want.Effect {
					t.Errorf("%q identity = %s/%s/%s; want %+v", argv, op.Family, op.Action, op.Effect, want)
				}
			}
		})
	}
}

func TestCommentsSpecializeAcrossLayers(t *testing.T) {
	for _, tc := range []struct{ command, kind string }{{"pr", "pullRequest"}, {"issue", "issue"}} {
		for _, verb := range []string{"create", "edit", "delete"} {
			t.Run(tc.command+"/"+verb, func(t *testing.T) {
				argv := []string{tc.command, "comment", "1", "--body", "body"}
				rest := []string{"api", "repos/o/r/issues/1/comments", "-f", "body=body"}
				field, input := "addComment", `subjectId:"opaque",body:"body"`
				if verb != "create" {
					argv = append(argv, "--"+verb+"-last")
					method := "PATCH"
					field, input = "updateIssueComment", `id:"opaque",body:"body"`
					if verb == "delete" {
						method, field, input = "DELETE", "deleteIssueComment", `id:"opaque"`
					}
					rest = []string{"api", "-X", method, "repos/o/r/issues/comments/2"}
				}
				native := operation(t, argv)
				want := tc.command + ".comment." + verb
				if native.Action != want || native.Target.Kind != tc.kind {
					t.Fatalf("native comment = %+v", native)
				}
				for _, args := range [][]string{rest, {"api", "graphql", "-f", "query=mutation { " + field + "(input:{" + input + "}) { clientMutationId } }"}} {
					op := operation(t, args)
					if op.Action != "comment."+verb || op.Family != "comment" || normalize.Specialize(op.Action, tc.kind) != want {
						t.Errorf("comment specialization = %+v, want %s", op, want)
					}
				}
			})
		}
	}
}

func TestSpecializeNeverGeneralizes(t *testing.T) {
	for _, tc := range []struct{ action, kind, want string }{
		{"comment.create", "pullRequest", "pr.comment.create"},
		{"comment.create", "issue", "issue.comment.create"},
		{"comment.create", "unknown", "comment.create"},
		{"comment.create", "repository", "comment.create"},
		{"comment.edit", "issue", "issue.comment.edit"},
		{"comment.delete", "pullRequest", "pr.comment.delete"},
		{"lockable.lock", "pullRequest", "pr.lock"},
		{"lockable.unlock", "issue", "issue.unlock"},
		{"pr.comment.create", "unknown", "pr.comment.create"},
		{"pr.comment.create", "issue", "pr.comment.create"},
	} {
		if got := normalize.Specialize(tc.action, tc.kind); got != tc.want {
			t.Errorf("Specialize(%q, %q) = %q; want %q", tc.action, tc.kind, got, tc.want)
		}
		if got := normalize.Specialize(tc.want, "unknown"); got != tc.want {
			t.Errorf("lost specialization: %q -> %q", tc.want, got)
		}
	}
}

func TestKnownActionPattern(t *testing.T) {
	for _, tc := range []struct {
		glob string
		want bool
	}{
		{"issue.comment", false}, {"pr.comment", false}, {"issue.comment.create", true},
		{"comment.*", true}, {"pr.merge.*", true}, {"pr.review.request-?hanges", true},
		{"rest.*", true}, {"graphql.*", true}, {"rest.codespaces.create-for-authenticated-user", true},
		{"graphql.createDiscussion", true}, {"graphql.updateIssue", false},
		{"graphql.query", true}, {"graphql.subscription", true},
		{"rest.codespaces.does-not-exist", false}, {"graphql.doesNotExist", false},
		{"*", true}, {"", false}, {"[", false}, {"no-such-action.*", false},
	} {
		if got := normalize.KnownActionPattern(tc.glob); got != tc.want {
			t.Errorf("KnownActionPattern(%q) = %v; want %v", tc.glob, got, tc.want)
		}
	}
}
