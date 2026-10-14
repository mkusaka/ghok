package rest

import (
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

type route struct {
	method, path, family, action string
	effect                       model.Effect
	segments                     []string
	literals                     int
}

// Method and entire segment sequence must match. A terminal * capture is
// used only for documented slash-bearing file paths and git refs.
var routes = compileRoutes([]route{
	{method: "GET", path: "/repos/{owner}/{repo}/pulls", family: "pr.read", action: "pr.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}", family: "pr.read", action: "pr.view"},
	{method: "POST", path: "/repos/{owner}/{repo}/pulls", family: "pr.create", action: "pr.create"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/pulls/{pull_number}", family: "pr.update", action: "pr.update"},
	{method: "PUT", path: "/repos/{owner}/{repo}/pulls/{pull_number}/merge", family: "pr.merge", action: "pr.merge.direct"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/merge", family: "pr.read", action: "pr.merge.status"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/commits", family: "pr.read", action: "pr.commits.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/files", family: "pr.read", action: "pr.files.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews", family: "pr.read", action: "pr.reviews.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews", family: "pr.review", action: "pr.review.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}", family: "pr.read", action: "pr.review.view"},
	{method: "PUT", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}", family: "pr.review", action: "pr.review.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}", family: "pr.review", action: "pr.review.delete"},
	{method: "POST", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}/events", family: "pr.review", action: "pr.review.submit"},
	{method: "PUT", path: "/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}/dismissals", family: "pr.review", action: "pr.review.dismiss"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers", family: "pr.read", action: "pr.reviewers.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers", family: "pr.reviewers", action: "pr.reviewers.request"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers", family: "pr.reviewers", action: "pr.reviewers.remove"},
	{method: "PUT", path: "/repos/{owner}/{repo}/pulls/{pull_number}/update-branch", family: "pr.update_branch", action: "pr.update_branch"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/comments", family: "pr.read", action: "pr.comments.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/comments/{comment_id}", family: "pr.read", action: "pr.comment.view"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/pulls/comments/{comment_id}", family: "pr.comment", action: "pr.comment.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/pulls/comments/{comment_id}", family: "pr.comment", action: "pr.comment.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/pulls/{pull_number}/comments", family: "pr.read", action: "pr.comments.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/pulls/{pull_number}/comments", family: "pr.comment", action: "pr.comment.create"},
	{method: "POST", path: "/repos/{owner}/{repo}/pulls/{pull_number}/comments/{comment_id}/replies", family: "pr.comment", action: "pr.comment.reply"},
	{method: "GET", path: "/repos/{owner}/{repo}/issues", family: "issue.read", action: "issue.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/issues", family: "issue.create", action: "issue.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/issues/{issue_number}", family: "issue.read", action: "issue.view"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/issues/{issue_number}", family: "issue.update", action: "issue.update"},
	{method: "PUT", path: "/repos/{owner}/{repo}/issues/{issue_number}/lock", family: "issue.lock", action: "issue.lock"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/issues/{issue_number}/lock", family: "issue.unlock", action: "issue.unlock"},
	{method: "GET", path: "/repos/{owner}/{repo}/issues/comments", family: "issue.read", action: "issue.comments.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/issues/comments/{comment_id}", family: "issue.read", action: "issue.comment.view"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/issues/comments/{comment_id}", family: "issue.comment", action: "issue.comment.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/issues/comments/{comment_id}", family: "issue.comment", action: "issue.comment.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/issues/{issue_number}/comments", family: "issue.read", action: "issue.comments.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/issues/{issue_number}/comments", family: "issue.comment", action: "issue.comment.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/issues/{issue_number}/labels", family: "issue.read", action: "issue.labels.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/issues/{issue_number}/labels", family: "issue.labels", action: "issue.labels.add"},
	{method: "PUT", path: "/repos/{owner}/{repo}/issues/{issue_number}/labels", family: "issue.labels", action: "issue.labels.replace"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/issues/{issue_number}/labels", family: "issue.labels", action: "issue.labels.clear"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/issues/{issue_number}/labels/{name}", family: "issue.labels", action: "issue.labels.remove"},
	{method: "POST", path: "/repos/{owner}/{repo}/issues/{issue_number}/assignees", family: "issue.assignees", action: "issue.assignees.add"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/issues/{issue_number}/assignees", family: "issue.assignees", action: "issue.assignees.remove"},
	{method: "GET", path: "/repos/{owner}/{repo}/labels", family: "repo.read", action: "repo.labels.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/labels", family: "repo.labels", action: "repo.labels.create"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/labels/{name}", family: "repo.labels", action: "repo.labels.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/labels/{name}", family: "repo.labels", action: "repo.labels.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}", family: "repo.read", action: "repo.view"},
	{method: "PATCH", path: "/repos/{owner}/{repo}", family: "repo.update", action: "repo.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}", family: "repo.delete", action: "repo.delete"},
	{method: "GET", path: "/user/repos", family: "repo.read", action: "repo.list"},
	{method: "POST", path: "/user/repos", family: "repo.create", action: "repo.create"},
	{method: "GET", path: "/orgs/{org}/repos", family: "repo.read", action: "repo.list"},
	{method: "POST", path: "/orgs/{org}/repos", family: "repo.create", action: "repo.create"},
	{method: "GET", path: "/users/{username}/repos", family: "repo.read", action: "repo.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/transfer", family: "repo.transfer", action: "repo.transfer"},
	{method: "POST", path: "/repos/{owner}/{repo}/forks", family: "repo.fork", action: "repo.fork"},
	{method: "GET", path: "/repos/{owner}/{repo}/forks", family: "repo.read", action: "repo.forks.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/collaborators", family: "repo.read", action: "repo.collaborators.list"},
	{method: "PUT", path: "/repos/{owner}/{repo}/collaborators/{username}", family: "repo.collaborators", action: "repo.collaborators.add"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/collaborators/{username}", family: "repo.collaborators", action: "repo.collaborators.remove"},
	{method: "POST", path: "/repos/{owner}/{repo}/git/refs", family: "ref.create", action: "ref.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/git/ref/{*ref}", family: "ref.read", action: "ref.view"},
	{method: "GET", path: "/repos/{owner}/{repo}/git/matching-refs/{*ref}", family: "ref.read", action: "ref.list"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/git/refs/{*ref}", family: "ref.update", action: "ref.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/git/refs/{*ref}", family: "ref.delete", action: "ref.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/branches", family: "branch.read", action: "branch.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/branches/{branch}", family: "branch.read", action: "branch.view"},
	{method: "GET", path: "/repos/{owner}/{repo}/branches/{branch}/protection", family: "branch.protection", action: "branch.protection.view"},
	{method: "PUT", path: "/repos/{owner}/{repo}/branches/{branch}/protection", family: "branch.protection", action: "branch.protection.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/branches/{branch}/protection", family: "branch.protection", action: "branch.protection.delete"},
	{method: "POST", path: "/repos/{owner}/{repo}/branches/{branch}/protection/enforce_admins", family: "branch.protection", action: "branch.protection.admins.enable"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/branches/{branch}/protection/enforce_admins", family: "branch.protection", action: "branch.protection.admins.disable"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews", family: "branch.protection", action: "branch.protection.reviews.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews", family: "branch.protection", action: "branch.protection.reviews.delete"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/branches/{branch}/protection/required_status_checks", family: "branch.protection", action: "branch.protection.checks.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/branches/{branch}/protection/required_status_checks", family: "branch.protection", action: "branch.protection.checks.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/rulesets", family: "repo.read", action: "repo.rulesets.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/rulesets", family: "repo.rulesets", action: "repo.rulesets.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/rulesets/{ruleset_id}", family: "repo.read", action: "repo.rulesets.view"},
	{method: "PUT", path: "/repos/{owner}/{repo}/rulesets/{ruleset_id}", family: "repo.rulesets", action: "repo.rulesets.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/rulesets/{ruleset_id}", family: "repo.rulesets", action: "repo.rulesets.delete"},
	{method: "GET", path: "/orgs/{org}/rulesets", family: "org.read", action: "org.rulesets.list"},
	{method: "POST", path: "/orgs/{org}/rulesets", family: "org.rulesets", action: "org.rulesets.create"},
	{method: "PUT", path: "/orgs/{org}/rulesets/{ruleset_id}", family: "org.rulesets", action: "org.rulesets.update"},
	{method: "DELETE", path: "/orgs/{org}/rulesets/{ruleset_id}", family: "org.rulesets", action: "org.rulesets.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/contents", family: "file.read", action: "file.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/contents/{*path}", family: "file.read", action: "file.view"},
	{method: "PUT", path: "/repos/{owner}/{repo}/contents/{*path}", family: "file.put", action: "file.put"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/contents/{*path}", family: "file.delete", action: "file.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/releases", family: "release.read", action: "release.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/releases", family: "release.create", action: "release.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/releases/latest", family: "release.read", action: "release.latest"},
	{method: "GET", path: "/repos/{owner}/{repo}/releases/tags/{tag}", family: "release.read", action: "release.tag.view"},
	{method: "GET", path: "/repos/{owner}/{repo}/releases/{release_id}", family: "release.read", action: "release.view"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/releases/{release_id}", family: "release.update", action: "release.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/releases/{release_id}", family: "release.delete", action: "release.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/releases/{release_id}/assets", family: "release.read", action: "release.assets.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/releases/{release_id}/assets", family: "release.asset", action: "release.asset.upload"},
	{method: "GET", path: "/repos/{owner}/{repo}/releases/assets/{asset_id}", family: "release.read", action: "release.asset.view"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/releases/assets/{asset_id}", family: "release.asset", action: "release.asset.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/releases/assets/{asset_id}", family: "release.asset", action: "release.asset.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/actions/workflows", family: "workflow.read", action: "workflow.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/actions/workflows/{workflow_id}", family: "workflow.read", action: "workflow.view"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/workflows/{workflow_id}/dispatches", family: "workflow.dispatch", action: "workflow.dispatch"},
	{method: "PUT", path: "/repos/{owner}/{repo}/actions/workflows/{workflow_id}/enable", family: "workflow.enable", action: "workflow.enable"},
	{method: "PUT", path: "/repos/{owner}/{repo}/actions/workflows/{workflow_id}/disable", family: "workflow.disable", action: "workflow.disable"},
	{method: "GET", path: "/repos/{owner}/{repo}/actions/runs", family: "run.read", action: "run.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/actions/runs/{run_id}", family: "run.read", action: "run.view"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/runs/{run_id}/rerun", family: "run.rerun", action: "run.rerun"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/runs/{run_id}/rerun-failed-jobs", family: "run.rerun", action: "run.rerun.failed"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/runs/{run_id}/cancel", family: "run.cancel", action: "run.cancel"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/runs/{run_id}/force-cancel", family: "run.cancel", action: "run.cancel.force"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/actions/runs/{run_id}", family: "run.delete", action: "run.delete"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/actions/runs/{run_id}/logs", family: "run.logs", action: "run.logs.delete"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/jobs/{job_id}/rerun", family: "run.rerun", action: "run.job.rerun"},
	{method: "GET", path: "/repos/{owner}/{repo}/actions/secrets", family: "secret.read", action: "secret.list"},
	{method: "PUT", path: "/repos/{owner}/{repo}/actions/secrets/{secret_name}", family: "secret.put", action: "secret.put"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/actions/secrets/{secret_name}", family: "secret.delete", action: "secret.delete"},
	{method: "GET", path: "/orgs/{org}/actions/secrets", family: "secret.read", action: "secret.list"},
	{method: "PUT", path: "/orgs/{org}/actions/secrets/{secret_name}", family: "secret.put", action: "secret.put"},
	{method: "DELETE", path: "/orgs/{org}/actions/secrets/{secret_name}", family: "secret.delete", action: "secret.delete"},
	{method: "PUT", path: "/orgs/{org}/actions/secrets/{secret_name}/repositories", family: "secret.repositories", action: "secret.repositories.replace"},
	{method: "PUT", path: "/orgs/{org}/actions/secrets/{secret_name}/repositories/{repository_id}", family: "secret.repositories", action: "secret.repositories.add"},
	{method: "DELETE", path: "/orgs/{org}/actions/secrets/{secret_name}/repositories/{repository_id}", family: "secret.repositories", action: "secret.repositories.remove"},
	{method: "PUT", path: "/repos/{owner}/{repo}/environments/{environment}/secrets/{secret_name}", family: "secret.put", action: "secret.put"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/environments/{environment}/secrets/{secret_name}", family: "secret.delete", action: "secret.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/actions/variables", family: "variable.read", action: "variable.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/actions/variables", family: "variable.create", action: "variable.create"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/actions/variables/{name}", family: "variable.update", action: "variable.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/actions/variables/{name}", family: "variable.delete", action: "variable.delete"},
	{method: "POST", path: "/orgs/{org}/actions/variables", family: "variable.create", action: "variable.create"},
	{method: "PATCH", path: "/orgs/{org}/actions/variables/{name}", family: "variable.update", action: "variable.update"},
	{method: "DELETE", path: "/orgs/{org}/actions/variables/{name}", family: "variable.delete", action: "variable.delete"},
	{method: "PUT", path: "/orgs/{org}/actions/variables/{name}/repositories", family: "variable.repositories", action: "variable.repositories.replace"},
	{method: "PUT", path: "/orgs/{org}/actions/variables/{name}/repositories/{repository_id}", family: "variable.repositories", action: "variable.repositories.add"},
	{method: "DELETE", path: "/orgs/{org}/actions/variables/{name}/repositories/{repository_id}", family: "variable.repositories", action: "variable.repositories.remove"},
	{method: "POST", path: "/repos/{owner}/{repo}/environments/{environment}/variables", family: "variable.create", action: "variable.create"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/environments/{environment}/variables/{name}", family: "variable.update", action: "variable.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/environments/{environment}/variables/{name}", family: "variable.delete", action: "variable.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/hooks", family: "hook.read", action: "hook.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/hooks", family: "hook.create", action: "hook.create"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/hooks/{hook_id}", family: "hook.update", action: "hook.update"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/hooks/{hook_id}", family: "hook.delete", action: "hook.delete"},
	{method: "POST", path: "/repos/{owner}/{repo}/hooks/{hook_id}/pings", family: "hook.ping", action: "hook.ping"},
	{method: "POST", path: "/repos/{owner}/{repo}/hooks/{hook_id}/tests", family: "hook.test", action: "hook.test"},
	{method: "POST", path: "/orgs/{org}/hooks", family: "hook.create", action: "hook.create"},
	{method: "PATCH", path: "/orgs/{org}/hooks/{hook_id}", family: "hook.update", action: "hook.update"},
	{method: "DELETE", path: "/orgs/{org}/hooks/{hook_id}", family: "hook.delete", action: "hook.delete"},
	{method: "GET", path: "/repos/{owner}/{repo}/keys", family: "key.read", action: "repo.keys.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/keys", family: "key.create", action: "repo.keys.create"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/keys/{key_id}", family: "key.delete", action: "repo.keys.delete"},
	{method: "GET", path: "/user/keys", family: "key.read", action: "user.keys.list"},
	{method: "POST", path: "/user/keys", family: "key.create", action: "user.keys.create"},
	{method: "DELETE", path: "/user/keys/{key_id}", family: "key.delete", action: "user.keys.delete"},
	{method: "POST", path: "/user/gpg_keys", family: "key.create", action: "user.gpg_keys.create"},
	{method: "DELETE", path: "/user/gpg_keys/{key_id}", family: "key.delete", action: "user.gpg_keys.delete"},
	{method: "POST", path: "/user/ssh_signing_keys", family: "key.create", action: "user.ssh_signing_keys.create"},
	{method: "DELETE", path: "/user/ssh_signing_keys/{key_id}", family: "key.delete", action: "user.ssh_signing_keys.delete"},
	{method: "GET", path: "/gists", family: "gist.read", action: "gist.list"},
	{method: "POST", path: "/gists", family: "gist.create", action: "gist.create"},
	{method: "GET", path: "/gists/{gist}", family: "gist.read", action: "gist.view"},
	{method: "PATCH", path: "/gists/{gist}", family: "gist.update", action: "gist.update"},
	{method: "DELETE", path: "/gists/{gist}", family: "gist.delete", action: "gist.delete"},
	{method: "PUT", path: "/gists/{gist}/star", family: "gist.star", action: "gist.star"},
	{method: "DELETE", path: "/gists/{gist}/star", family: "gist.star", action: "gist.unstar"},
	{method: "POST", path: "/gists/{gist}/forks", family: "gist.fork", action: "gist.fork"},
	{method: "POST", path: "/gists/{gist}/comments", family: "gist.comment", action: "gist.comment.create"},
	{method: "PATCH", path: "/gists/{gist}/comments/{comment_id}", family: "gist.comment", action: "gist.comment.update"},
	{method: "DELETE", path: "/gists/{gist}/comments/{comment_id}", family: "gist.comment", action: "gist.comment.delete"},
	{method: "GET", path: "/orgs/{org}/members", family: "org.read", action: "org.members.list"},
	{method: "DELETE", path: "/orgs/{org}/members/{username}", family: "org.members", action: "org.members.remove"},
	{method: "PUT", path: "/orgs/{org}/memberships/{username}", family: "org.members", action: "org.membership.put"},
	{method: "DELETE", path: "/orgs/{org}/memberships/{username}", family: "org.members", action: "org.membership.delete"},
	{method: "PUT", path: "/orgs/{org}/public_members/{username}", family: "org.members", action: "org.membership.publicize"},
	{method: "DELETE", path: "/orgs/{org}/public_members/{username}", family: "org.members", action: "org.membership.conceal"},
	{method: "PATCH", path: "/orgs/{org}", family: "org.update", action: "org.update"},
	{method: "GET", path: "/orgs/{org}/teams", family: "team.read", action: "team.list"},
	{method: "POST", path: "/orgs/{org}/teams", family: "team.create", action: "team.create"},
	{method: "PATCH", path: "/orgs/{org}/teams/{team_slug}", family: "team.update", action: "team.update"},
	{method: "DELETE", path: "/orgs/{org}/teams/{team_slug}", family: "team.delete", action: "team.delete"},
	{method: "PUT", path: "/orgs/{org}/teams/{team_slug}/memberships/{username}", family: "team.members", action: "team.membership.put"},
	{method: "DELETE", path: "/orgs/{org}/teams/{team_slug}/memberships/{username}", family: "team.members", action: "team.membership.delete"},
	{method: "PUT", path: "/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}", family: "team.repositories", action: "team.repository.put"},
	{method: "DELETE", path: "/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}", family: "team.repositories", action: "team.repository.delete"},
	{method: "POST", path: "/repos/{owner}/{repo}/merges", family: "branch.merge", action: "branch.merge"},
	{method: "POST", path: "/repos/{owner}/{repo}/check-runs", family: "check.create", action: "check.create"},
	{method: "PATCH", path: "/repos/{owner}/{repo}/check-runs/{check_id}", family: "check.update", action: "check.update"},
	{method: "POST", path: "/repos/{owner}/{repo}/statuses/{sha}", family: "status.create", action: "status.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/deployments", family: "deployment.read", action: "deployment.list"},
	{method: "POST", path: "/repos/{owner}/{repo}/deployments", family: "deployment.create", action: "deployment.create"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/deployments/{deployment_id}", family: "deployment.delete", action: "deployment.delete"},
	{method: "POST", path: "/repos/{owner}/{repo}/deployments/{deployment_id}/statuses", family: "deployment.status", action: "deployment.status.create"},
	{method: "GET", path: "/repos/{owner}/{repo}/environments", family: "environment.read", action: "environment.list"},
	{method: "GET", path: "/repos/{owner}/{repo}/environments/{environment}", family: "environment.read", action: "environment.view"},
	{method: "PUT", path: "/repos/{owner}/{repo}/environments/{environment}", family: "environment.put", action: "environment.put"},
	{method: "DELETE", path: "/repos/{owner}/{repo}/environments/{environment}", family: "environment.delete", action: "environment.delete"},
	{method: "GET", path: "/user", family: "user.read", action: "user.view"},
	{method: "PATCH", path: "/user", family: "user.update", action: "user.update"},
	{method: "GET", path: "/notifications", family: "notification.read", action: "notification.list"},
	{method: "PUT", path: "/notifications", family: "notification.mark_read", action: "notification.mark_read"},
	{method: "PUT", path: "/repos/{owner}/{repo}/notifications", family: "notification.mark_read", action: "notification.mark_read"},
	{method: "PATCH", path: "/notifications/threads/{thread_id}", family: "notification.mark_read", action: "notification.mark_read"},
	{method: "DELETE", path: "/notifications/threads/{thread_id}", family: "notification.done", action: "notification.done"},
})

func compileRoutes(rs []route) map[string][]route {
	byMethod := make(map[string][]route)
	for _, r := range rs {
		r.segments = strings.Split(strings.TrimPrefix(r.path, "/"), "/")
		r.effect = methodEffect(r.method)
		for _, s := range r.segments {
			if !strings.HasPrefix(s, "{") {
				r.literals++
			}
		}
		byMethod[r.method] = append(byMethod[r.method], r)
	}
	return byMethod
}

func matchRoute(method string, segments []string) (*route, map[string]string) {
	var best *route
	for i := range routes[method] {
		r := &routes[method][i]
		if routeMatches(r, segments) && (best == nil || r.literals > best.literals) {
			best = r
		}
	}
	if best == nil {
		return nil, nil
	}
	captures := make(map[string]string)
	for i, pattern := range best.segments {
		if !strings.HasPrefix(pattern, "{") {
			continue
		}
		name := strings.Trim(pattern, "{}")
		if strings.HasPrefix(name, "*") {
			captures[name[1:]] = strings.Join(segments[i:], "/")
		} else {
			captures[name] = segments[i]
		}
	}
	return best, captures
}

func routeMatches(r *route, segments []string) bool {
	tail := strings.HasPrefix(r.segments[len(r.segments)-1], "{*")
	if (!tail && len(segments) != len(r.segments)) || (tail && len(segments) < len(r.segments)) {
		return false
	}
	for i, pattern := range r.segments {
		if strings.HasPrefix(pattern, "{*") {
			return true
		}
		if !strings.HasPrefix(pattern, "{") {
			if pattern != segments[i] {
				return false
			}
		} else if !validCapture(strings.Trim(pattern, "{}"), segments[i]) {
			return false
		}
	}
	return true
}

func validCapture(name, value string) bool {
	if value == "" {
		return false
	}
	if name == "branch" || name == "environment" || name == "name" || name == "tag" {
		return !strings.ContainsAny(value, "{}") || (name == "branch" && value == "{branch}")
	}
	if strings.ContainsRune(value, '/') {
		return false
	}
	if hasPlaceholder(value) {
		return (name == "owner" && (value == "{owner}" || value == ":owner")) || (name == "repo" && (value == "{repo}" || value == ":repo"))
	}
	if strings.ContainsAny(value, "{}") {
		return false
	}
	if name == "workflow_id" && (strings.HasSuffix(value, ".yml") || strings.HasSuffix(value, ".yaml")) {
		return true
	}
	if strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "_number") {
		for _, c := range value {
			if c < '0' || c > '9' {
				return false
			}
		}
		n, err := strconv.Atoi(value)
		return err == nil && n > 0
	}
	return true
}
