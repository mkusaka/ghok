# Operation normalization

`ghok` classifies **requested intent**, not confirmed GitHub side effects. Native
commands (L1), REST requests (L2), and GraphQL documents (L3) produce a
`schemaVersion: 1` result containing one or more operations. An exact classification
can still have an unresolved target, and a known write can still be rejected by
`gh` or GitHub. Unknown is not a synonym for safe.

For policy and hook authors: match the operation's **`action`**, inspect `effect`,
`execution`, and `unresolved`, and do not use a family alone to infer a concrete
side effect. Hooks are trusted executable code; neither normalization nor hooks
are a security boundary.

Inspect without running the original command or hooks:

```sh
ghok normalize -- pr merge 123 --squash
ghok explain -- api -X PUT repos/org/repo/pulls/123/merge -f merge_method=squash
```

Both commands redact output by default. `--resolve` adds local target resolution;
`--resolve --allow-api` also permits read-only API resolution. Without those
options, normalization does not resolve git/API context. It can still read
explicitly supplied API files or non-TTY stdin for analysis. The implementation's
CLI/API baseline is **gh 2.102.0**, not an assertion of coverage for every future
command, REST route, or GitHub Enterprise Server version.

## Result and operation contract

The authoritative contracts are [model.go](../internal/model/model.go) and
[invocation.go](../internal/model/invocation.go). Normalization returns:

| Result field | Meaning |
| --- | --- |
| `schemaVersion` | `1`, shared with the hook protocol. |
| `classification` | Aggregate `level`, `confidence`, `resolution`, and `mutationFree`. |
| `operations` | One semantic operation per native/REST invocation; GraphQL can enumerate several root mutation fields, including fields that will not execute. |
| `source` | Syntactic source information, described below. |
| `errors` | Optional non-fatal analysis errors. Inspect the operations as well; unavailable REST bodies can be unresolved without an entry here. |

Every operation has the following fields. Optional fields are omitted when empty.

| Operation field | Meaning |
| --- | --- |
| `family` | Semantic grouping, for example `pr.merge`; `unknown` when no semantic mapping is available. Families are not uniformly identical across layers. |
| `action` | Specific identity used by hook/policy operation globs, for example `pr.merge.queue.enqueue`. |
| `effect` | `read`, `write`, or `unknown`; a classification, not a success/outcome report. |
| `target` | Known host/repository/resource selectors. An empty target does not prove a target does not exist. |
| `parameters` | Optional map of relevant argv/request/input values; representation and canonical keys depend on the layer. |
| `execution` | `will-run`, `maybe`, `not-selected`, or `skipped`; see below. |
| `evidence` | Optional explanation of non-default execution or other proofs. |
| `unresolved` | Optional list of missing/ambiguous values, each with `field`, `reason`, and optional `source` such as `git`, `api`, `variable`, `stdin`, or `file`. |
| `unknownReason` | Machine-readable reason when family is `unknown` or effect is `unknown`. |
| `detail` | Optional human-readable explanation; not a stable matching key. |
| `origin` | Canonical command path, matched REST method/route, or GraphQL schema field/response alias/operation name/type. |

`target` supports `host`, `owner`, `repository` (`owner/name`), `number` (PR/issue),
`nodeId`, `branch`, `baseBranch`, `headBranch`, `sha`, `ref`, and `url`. Empty strings
and absent numbers mean not known. `nodeId` can identify a PR, review, repository,
ref, comment, or other resource; it is not necessarily a repository ID.

`origin` has optional `command` (array), `method`, `route`, `field`, `alias`,
`operation`, and `opType` (`query`, `mutation`, or `subscription`). A GraphQL response
alias is recorded separately and never replaces the schema field identity.

`source` has `kind` (`command`, `rest`, `graphql`, or `unknown`) and optional `argv`,
`alias`, `method`, `path`, `host`, and `graphql`. Dispatcher argv describes the
alias-expanded analysis command; real `gh` still receives the original argv.
REST paths are normalized display paths; GraphQL paths retain the endpoint
spelling. GraphQL source text is capped at 64 KiB. Variables are not added to that
text, but literals/defaults already embedded in the document remain there.

**Redaction is an output-boundary operation.** Although the model describes source
as redaction-safe, raw normalizer results can contain sensitive argv, API body,
and GraphQL input values. Use the CLI's redacted output or the shared redactor;
do not directly emit internal results to logs or untrusted consumers. See
[security](security.md) for the trust model.

### Level, confidence, resolution, and execution

These dimensions answer different questions:

| Dimension | Values and interpretation |
| --- | --- |
| Level | `L1`: native command; `L2`: REST; `L3`: GraphQL AST; `none`: no layer could classify an internal normalizer failure. Unknowns can still have a known layer. |
| Confidence | `exact`: command/typed route/AST identity is syntactically known; `heuristic`: inferred identity or uncertain native option parsing; `none`: no recognized classification. Exact does not mean schema-valid, fully resolved, or successful. |
| Resolution | `complete`: no required missing values reported; `deferred`: context lookup remains; `partial`: some input/selector data unavailable; `unresolved`: analysis cannot resolve the request. Use individual `unresolved` entries for actionable details. |
| Execution | `will-run`: request intent is selected, not proof of acceptance; `maybe`: a GraphQL execution condition is unknown/nonboolean; `not-selected`: another GraphQL operation was chosen; `skipped`: directives prove non-execution, or recognized malformed REST construction is rejected before HTTP. |
| Mutation-free | `true` only for the layer's no-write classification/proof; this is separate from target completeness and actual command success. |

The aggregate resolution values are **not implemented identically in all layers**:

- L1 reports `deferred` for only git/API gaps, `partial` for other gaps, and
  `unresolved` for unknown effects. A known command with an unknown flag retains
  its semantic effect/family/action but becomes `heuristic`; read effect alone is
  then insufficient for `mutationFree`.
- L2 reports `deferred` for git or git/config gaps and `partial` for other gaps.
  A normal unregistered route need not be `unresolved`: it can have a complete
  parameter/host description but no known semantic mapping. Early parse/path
  failures use the single-unknown result (`none` confidence, `unresolved`
  resolution).
- Successful L3 analysis uses `deferred` for **any** unresolved entry, including
  missing variables/input, not just later-resolvable context. Its errors use
  `unresolved`. Always consult the entries rather than treating `deferred` as a
  promise that the resolver can recover every field.

For L1, exact reads are mutation-free. For L2, reads on accepted GitHub REST path
shapes are mutation-free, including unregistered GET/HEAD by convention. Opaque
absolute hosts, method override, and malformed requests are not safe reads. For
L3, a selected query is mutation-free; a selected mutation is mutation-free only
when every root mutation is proven skipped. Non-selected mutation fields remain
in the array but do not defeat that proof. Subscriptions and analysis failures
are not mutation-free.

### Unknown reasons

| `unknownReason` | Meaning/current use |
| --- | --- |
| `unknown-command` | Command absent from L1 registry, or a recognized command whose runtime effects are not normalized (Copilot, selected codespace integrations, hidden completion, telemetry sender). |
| `unknown-flag` | Root-only unrecognized flag leaves command identity unknown. Known leaves generally retain intent with heuristic confidence instead. |
| `extension` | `extension exec` forwards to external code without flag parsing. Direct unregistered extension spellings use `unknown-command`. |
| `shell-alias` | Configured alias begins with `!`; no expansion/execution is attempted for analysis. |
| `alias-loop` | Alias full-argv cycle or more than 64 expansion steps. |
| `unknown-endpoint` | Unmapped REST write/unknown route, opaque absolute REST URL, or non-literal relative GraphQL endpoint routed as REST on a known GHES host. |
| `unknown-mutation` | GraphQL root mutation lacks semantic mapping, whether present or absent in the embedded schema snapshot. It remains a write. |
| `parse-error` | Argument/alias lexer/JSON/GraphQL parse failure or recovered internal normalizer panic. API unknown flags also take this path. |
| `input-unavailable` | GraphQL query/body/operation name cannot be read, including TTY or timed-out stdin. REST unavailable bodies ordinarily preserve the route and add unresolved parameters instead. |
| `input-too-large` | GraphQL document/input/selection/depth bound exceeded, or recognizable input reader size-limit failure. |
| `operation-ambiguous` | Multiple GraphQL operations without selection; REST `X-HTTP-Method-Override` also uses this reason because server-side method is uncertain. |
| `malformed-request` | Structurally invalid input, unsupported subscription/batching, invalid path, invalid request field/header construction, or recognized rejected REST pagination combination. |

Unknown actions are not always just `unknown`: unmapped REST requests use
`api.<lowercase-method>.unknown`; unmapped GraphQL fields use
`graphql.mutation.<schemaField>`. Unregistered GET/HEAD uses `api.read` /
`api.read.unknown`, heuristic read, with no `unknownReason`.

## Naming and merge comparison

Native names usually replace spaces in canonical command paths with dots, keeping
hyphens in verbs (`pr.update-branch`). Groups/help topics become `help`. API
mappings group semantic reads differently (`pr.read`, `repo.read`), so do not
assume every equal action has an equal family or vice versa.

Review rejection is `pr.review.request-changes` in all three layers.

All examples below have family `pr.merge` and effect `write`, but differ in intent:

| Invocation/input | Action | Canonical method/target distinction |
| --- | --- | --- |
| `gh pr merge 123 --squash` | `pr.merge.request` | `method: squash`; number known, repository may still need context. Server/runtime can choose direct merge, auto-merge, or merge queue. |
| `gh pr merge 123 --admin --squash` | `pr.merge.direct` | Explicit direct/admin intent, not proof protection is bypassed or a merge succeeds. |
| `gh pr merge 123 --auto` | `pr.merge.auto.enable` | Requests auto-merge; no merge method is invented when no method flag is given. |
| `gh pr merge 123 --disable-auto` | `pr.merge.auto.disable` | Disables auto-merge intent; not a direct merge. |
| `gh api -X PUT repos/o/r/pulls/123/merge -f merge_method=squash` | `pr.merge.direct` | REST body `merge_method` becomes `method`; repository/number come from the route. |
| `mergePullRequest(input:{pullRequestId:$id,mergeMethod:SQUASH})` | `pr.merge.direct` | `method: squash`; original `mergeMethod` retained, node ID can be unresolved and repository requires lookup. |
| `enablePullRequestAutoMerge(input:{pullRequestId:$id})` | `pr.merge.auto.enable` | Omitted method uses schema default `method: merge`; supplied unavailable/null method stays unresolved, never defaults. |
| `enqueuePullRequest(input:{pullRequestId:$id})` | `pr.merge.queue.enqueue` | Queue request, not direct merge or auto-merge. |
| `dequeuePullRequest(input:{id:$id})` | `pr.merge.queue.dequeue` | Queue removal; the PR input field is `id`, not `pullRequestId`. |

L1 `method` is an argv-derived string (empty when unspecified); L2 only adds it
when the merge body contains `merge_method`; L3 lowercases `mergeMethod` and uses
the schema default when absent from an available input object. L1 records
`admin`, `deleteBranch`, subject, head-commit matching, and body/body-file
**presence**. L3 retains input and adds `commitHeadlinePresent` and
`commitBodyPresent`. These representations are intentionally not byte-for-byte
equivalent. `mergeBranch` is `branch.merge`, never `pr.merge`.

## L1: native commands, aliases, and extensions

[Native normalizer](../internal/normalize/native/native.go),
[target extractors](../internal/normalize/native/targets.go), and
[registry](../internal/normalize/native/registry.go) describe 243 registered paths
and 57 built-in alias spellings. The table includes groups, hidden help topics,
and completion entry points; registered identity is not a guarantee that effects
are known or targets are deeply extracted.

Supported argument forms include interspersed flags, options before subcommands,
`--flag=value`, `--flag value`, `-Rvalue`, `-R=value`, `-R value`, bundled short
booleans, explicit boolean values, repeated flags, and `--`. Registered `-h`
meanings take precedence over help (auth hostname, config host, repo homepage).
Help/version prove non-mutation only when parsed as options on a known command
with known effects; forwarded/unknown command `--help` does not become safe.

Repository/selector handling:

- Explicit repository positionals or PR/issue URLs override `--repo`, then
  `GH_REPO`. `GH_HOST` supplies the fallback host; explicit owner/name defaults
  to github.com without it. Repository URLs and `host/owner/name` are recognized.
- Positive number and `#number` PR/issue selectors are recognized. PR branches
  retain `headBranch` and defer PR-number lookup. Missing PR selector defers current
  branch and its PR; missing repository defers git context instead of assuming
  github.com. PR/issue URL selectors also recognize trailing paths/query/fragment.
- Org/user secret/variable and org ruleset scopes do not inherit the cwd repository.
  Other selectors without a target-model field stay in parameters: tag/assets,
  name/newName, workflow, run, ruleset, destination repository, or arguments.
- Flag parameter keys are lower camel case. Booleans are booleans; repeatable
  options remain arrays of literal argv strings, including unexpanded CSV.
  Other options remain strings. Native bodies/body files/env files record presence,
  not content/path. Workflow fields are string inputs, raw before typed; file/JSON
  stdin values are unresolved, not loaded. Sensitive auth/secret/variable commands
  carry presence indicators such as `sensitive`, `tokenFromStdin`, or `valueFromStdin`.

Configured plain aliases expand only at argv[0], using injected alias definitions,
with built-ins taking precedence. Numbered substitution happens before quote/escape
lexing; arguments append only once no dollar remains. The lexer has no shell
operator/environment expansion. Chains are expanded up to 64 steps; shell aliases,
cycles, missing numbered placeholders, and malformed quoting are explicit unknowns.
Only the first configured alias name is retained in `source.alias`. Configured
aliases nested under root flags are not expanded. The supplied alias map does not
model gh's registration order or installed-extension precedence over aliases.

`extension exec` retains forwarded arguments without parsing them; its effect is
unknown. Direct unknown commands may be extensions or newer gh commands; no
extension executable is analyzed. Copilot and codespace code/Jupyter/SSH integration
also have unknown effects even with exact command identity. Clone/checkout,
auth/config/install operations are conservative writes; downloads are GitHub reads
although they can write local files. This taxonomy is not local-filesystem isolation.

### L1 family/action table

The following rows give the default emitted action after native specialization.
`pr merge` has raw registry action `pr.merge`, but always specializes to
`pr.merge.request` or the flag-driven alternatives below. The empty root invocation
is an implicit `help` / `help` / read outside the 243 explicit registry paths.

| Command after `gh` | Family | Default action | Effect |
| --- | --- | --- | --- |
| `__complete` | `completion` | `completion` | unknown |
| `__completeNoDesc` | `completion` | `completion` | unknown |
| `accessibility` | `accessibility` | `accessibility` | read |
| `actions` | `actions` | `actions` | read |
| `agent-task` | `help` | `help` | read |
| `agent-task create` | `agent-task.create` | `agent-task.create` | write |
| `agent-task list` | `agent-task.list` | `agent-task.list` | read |
| `agent-task view` | `agent-task.view` | `agent-task.view` | read |
| `alias` | `help` | `help` | read |
| `alias delete` | `alias.delete` | `alias.delete` | write |
| `alias import` | `alias.import` | `alias.import` | write |
| `alias list` | `alias.list` | `alias.list` | read |
| `alias set` | `alias.set` | `alias.set` | write |
| `api` | `api` | `api` | unknown |
| `attestation` | `help` | `help` | read |
| `attestation download` | `attestation.download` | `attestation.download` | read |
| `attestation trusted-root` | `attestation.trusted-root` | `attestation.trusted-root` | read |
| `attestation verify` | `attestation.verify` | `attestation.verify` | read |
| `auth` | `help` | `help` | read |
| `auth login` | `auth.login` | `auth.login` | write |
| `auth logout` | `auth.logout` | `auth.logout` | write |
| `auth refresh` | `auth.refresh` | `auth.refresh` | write |
| `auth setup-git` | `auth.setup-git` | `auth.setup-git` | write |
| `auth status` | `auth.status` | `auth.status` | read |
| `auth switch` | `auth.switch` | `auth.switch` | write |
| `auth token` | `auth.token` | `auth.token` | read |
| `browse` | `browse` | `browse` | read |
| `cache` | `help` | `help` | read |
| `cache delete` | `cache.delete` | `cache.delete` | write |
| `cache list` | `cache.list` | `cache.list` | read |
| `co` | `pr.checkout` | `pr.checkout` | write |
| `codespace` | `help` | `help` | read |
| `codespace code` | `codespace.code` | `codespace.code` | unknown |
| `codespace cp` | `codespace.cp` | `codespace.cp` | write |
| `codespace create` | `codespace.create` | `codespace.create` | write |
| `codespace delete` | `codespace.delete` | `codespace.delete` | write |
| `codespace edit` | `codespace.edit` | `codespace.edit` | write |
| `codespace jupyter` | `codespace.jupyter` | `codespace.jupyter` | unknown |
| `codespace list` | `codespace.list` | `codespace.list` | read |
| `codespace logs` | `codespace.logs` | `codespace.logs` | read |
| `codespace ports` | `help` | `help` | read |
| `codespace ports forward` | `codespace.ports.forward` | `codespace.ports.forward` | write |
| `codespace ports visibility` | `codespace.ports.visibility` | `codespace.ports.visibility` | write |
| `codespace rebuild` | `codespace.rebuild` | `codespace.rebuild` | write |
| `codespace ssh` | `codespace.ssh` | `codespace.ssh` | unknown |
| `codespace stop` | `codespace.stop` | `codespace.stop` | write |
| `codespace view` | `codespace.view` | `codespace.view` | read |
| `completion` | `completion` | `completion` | read |
| `config` | `help` | `help` | read |
| `config clear-cache` | `config.clear-cache` | `config.clear-cache` | write |
| `config get` | `config.get` | `config.get` | read |
| `config list` | `config.list` | `config.list` | read |
| `config set` | `config.set` | `config.set` | write |
| `copilot` | `copilot` | `copilot` | unknown |
| `credits` | `credits` | `credits` | read |
| `discussion` | `help` | `help` | read |
| `discussion comment` | `discussion.comment` | `discussion.comment` | write |
| `discussion create` | `discussion.create` | `discussion.create` | write |
| `discussion edit` | `discussion.edit` | `discussion.edit` | write |
| `discussion list` | `discussion.list` | `discussion.list` | read |
| `discussion view` | `discussion.view` | `discussion.view` | read |
| `environment` | `help` | `help` | read |
| `exit-codes` | `help` | `help` | read |
| `extension` | `help` | `help` | read |
| `extension browse` | `extension.browse` | `extension.browse` | read |
| `extension create` | `extension.create` | `extension.create` | write |
| `extension exec` | `extension.exec` | `extension.exec` | unknown |
| `extension install` | `extension.install` | `extension.install` | write |
| `extension list` | `extension.list` | `extension.list` | read |
| `extension remove` | `extension.remove` | `extension.remove` | write |
| `extension search` | `extension.search` | `extension.search` | read |
| `extension upgrade` | `extension.upgrade` | `extension.upgrade` | write |
| `formatting` | `help` | `help` | read |
| `gist` | `help` | `help` | read |
| `gist clone` | `gist.clone` | `gist.clone` | write |
| `gist create` | `gist.create` | `gist.create` | write |
| `gist delete` | `gist.delete` | `gist.delete` | write |
| `gist edit` | `gist.edit` | `gist.edit` | write |
| `gist list` | `gist.list` | `gist.list` | read |
| `gist rename` | `gist.rename` | `gist.rename` | write |
| `gist view` | `gist.view` | `gist.view` | read |
| `gpg-key` | `help` | `help` | read |
| `gpg-key add` | `gpg-key.add` | `gpg-key.add` | write |
| `gpg-key delete` | `gpg-key.delete` | `gpg-key.delete` | write |
| `gpg-key list` | `gpg-key.list` | `gpg-key.list` | read |
| `help` | `help` | `help` | read |
| `issue` | `help` | `help` | read |
| `issue close` | `issue.close` | `issue.close` | write |
| `issue comment` | `issue.comment` | `issue.comment` | write |
| `issue create` | `issue.create` | `issue.create` | write |
| `issue delete` | `issue.delete` | `issue.delete` | write |
| `issue develop` | `issue.develop` | `issue.develop` | write |
| `issue edit` | `issue.edit` | `issue.edit` | write |
| `issue list` | `issue.list` | `issue.list` | read |
| `issue lock` | `issue.lock` | `issue.lock` | write |
| `issue pin` | `issue.pin` | `issue.pin` | write |
| `issue reopen` | `issue.reopen` | `issue.reopen` | write |
| `issue status` | `issue.status` | `issue.status` | read |
| `issue transfer` | `issue.transfer` | `issue.transfer` | write |
| `issue unlock` | `issue.unlock` | `issue.unlock` | write |
| `issue unpin` | `issue.unpin` | `issue.unpin` | write |
| `issue view` | `issue.view` | `issue.view` | read |
| `label` | `help` | `help` | read |
| `label clone` | `label.clone` | `label.clone` | write |
| `label create` | `label.create` | `label.create` | write |
| `label delete` | `label.delete` | `label.delete` | write |
| `label edit` | `label.edit` | `label.edit` | write |
| `label list` | `label.list` | `label.list` | read |
| `licenses` | `licenses` | `licenses` | read |
| `mintty` | `help` | `help` | read |
| `org` | `help` | `help` | read |
| `org list` | `org.list` | `org.list` | read |
| `pr` | `help` | `help` | read |
| `pr checkout` | `pr.checkout` | `pr.checkout` | write |
| `pr checks` | `pr.checks` | `pr.checks` | read |
| `pr close` | `pr.close` | `pr.close` | write |
| `pr comment` | `pr.comment` | `pr.comment` | write |
| `pr create` | `pr.create` | `pr.create` | write |
| `pr diff` | `pr.diff` | `pr.diff` | read |
| `pr edit` | `pr.edit` | `pr.edit` | write |
| `pr list` | `pr.list` | `pr.list` | read |
| `pr lock` | `pr.lock` | `pr.lock` | write |
| `pr merge` | `pr.merge` | `pr.merge.request` | write |
| `pr ready` | `pr.ready` | `pr.ready` | write |
| `pr reopen` | `pr.reopen` | `pr.reopen` | write |
| `pr revert` | `pr.revert` | `pr.revert` | write |
| `pr review` | `pr.review` | `pr.review` | write |
| `pr status` | `pr.status` | `pr.status` | read |
| `pr unlock` | `pr.unlock` | `pr.unlock` | write |
| `pr update-branch` | `pr.update-branch` | `pr.update-branch` | write |
| `pr view` | `pr.view` | `pr.view` | read |
| `preview` | `help` | `help` | read |
| `preview prompter` | `preview.prompter` | `preview.prompter` | read |
| `project` | `help` | `help` | read |
| `project close` | `project.close` | `project.close` | write |
| `project copy` | `project.copy` | `project.copy` | write |
| `project create` | `project.create` | `project.create` | write |
| `project delete` | `project.delete` | `project.delete` | write |
| `project edit` | `project.edit` | `project.edit` | write |
| `project field-create` | `project.field-create` | `project.field-create` | write |
| `project field-delete` | `project.field-delete` | `project.field-delete` | write |
| `project field-list` | `project.field-list` | `project.field-list` | read |
| `project item-add` | `project.item-add` | `project.item-add` | write |
| `project item-archive` | `project.item-archive` | `project.item-archive` | write |
| `project item-create` | `project.item-create` | `project.item-create` | write |
| `project item-delete` | `project.item-delete` | `project.item-delete` | write |
| `project item-edit` | `project.item-edit` | `project.item-edit` | write |
| `project item-list` | `project.item-list` | `project.item-list` | read |
| `project link` | `project.link` | `project.link` | write |
| `project list` | `project.list` | `project.list` | read |
| `project mark-template` | `project.mark-template` | `project.mark-template` | write |
| `project unlink` | `project.unlink` | `project.unlink` | write |
| `project view` | `project.view` | `project.view` | read |
| `reference` | `help` | `help` | read |
| `release` | `help` | `help` | read |
| `release create` | `release.create` | `release.create` | write |
| `release delete` | `release.delete` | `release.delete` | write |
| `release delete-asset` | `release.delete-asset` | `release.delete-asset` | write |
| `release download` | `release.download` | `release.download` | read |
| `release edit` | `release.edit` | `release.edit` | write |
| `release list` | `release.list` | `release.list` | read |
| `release upload` | `release.upload` | `release.upload` | write |
| `release verify` | `release.verify` | `release.verify` | read |
| `release verify-asset` | `release.verify-asset` | `release.verify-asset` | read |
| `release view` | `release.view` | `release.view` | read |
| `repo` | `help` | `help` | read |
| `repo archive` | `repo.archive` | `repo.archive` | write |
| `repo autolink` | `help` | `help` | read |
| `repo autolink create` | `repo.autolink.create` | `repo.autolink.create` | write |
| `repo autolink delete` | `repo.autolink.delete` | `repo.autolink.delete` | write |
| `repo autolink list` | `repo.autolink.list` | `repo.autolink.list` | read |
| `repo autolink view` | `repo.autolink.view` | `repo.autolink.view` | read |
| `repo clone` | `repo.clone` | `repo.clone` | write |
| `repo create` | `repo.create` | `repo.create` | write |
| `repo delete` | `repo.delete` | `repo.delete` | write |
| `repo deploy-key` | `help` | `help` | read |
| `repo deploy-key add` | `repo.deploy-key.add` | `repo.deploy-key.add` | write |
| `repo deploy-key delete` | `repo.deploy-key.delete` | `repo.deploy-key.delete` | write |
| `repo deploy-key list` | `repo.deploy-key.list` | `repo.deploy-key.list` | read |
| `repo edit` | `repo.edit` | `repo.edit` | write |
| `repo fork` | `repo.fork` | `repo.fork` | write |
| `repo gitignore` | `help` | `help` | read |
| `repo gitignore list` | `repo.gitignore.list` | `repo.gitignore.list` | read |
| `repo gitignore view` | `repo.gitignore.view` | `repo.gitignore.view` | read |
| `repo license` | `help` | `help` | read |
| `repo license list` | `repo.license.list` | `repo.license.list` | read |
| `repo license view` | `repo.license.view` | `repo.license.view` | read |
| `repo list` | `repo.list` | `repo.list` | read |
| `repo read-dir` | `repo.read-dir` | `repo.read-dir` | read |
| `repo read-file` | `repo.read-file` | `repo.read-file` | read |
| `repo rename` | `repo.rename` | `repo.rename` | write |
| `repo set-default` | `repo.set-default` | `repo.set-default` | write |
| `repo sync` | `repo.sync` | `repo.sync` | write |
| `repo unarchive` | `repo.unarchive` | `repo.unarchive` | write |
| `repo view` | `repo.view` | `repo.view` | read |
| `ruleset` | `help` | `help` | read |
| `ruleset check` | `ruleset.check` | `ruleset.check` | read |
| `ruleset list` | `ruleset.list` | `ruleset.list` | read |
| `ruleset view` | `ruleset.view` | `ruleset.view` | read |
| `run` | `help` | `help` | read |
| `run cancel` | `run.cancel` | `run.cancel` | write |
| `run delete` | `run.delete` | `run.delete` | write |
| `run download` | `run.download` | `run.download` | read |
| `run list` | `run.list` | `run.list` | read |
| `run rerun` | `run.rerun` | `run.rerun` | write |
| `run view` | `run.view` | `run.view` | read |
| `run watch` | `run.watch` | `run.watch` | read |
| `search` | `help` | `help` | read |
| `search code` | `search.code` | `search.code` | read |
| `search commits` | `search.commits` | `search.commits` | read |
| `search issues` | `search.issues` | `search.issues` | read |
| `search prs` | `search.prs` | `search.prs` | read |
| `search repos` | `search.repos` | `search.repos` | read |
| `secret` | `help` | `help` | read |
| `secret delete` | `secret.delete` | `secret.delete` | write |
| `secret list` | `secret.list` | `secret.list` | read |
| `secret set` | `secret.set` | `secret.set` | write |
| `send-telemetry` | `send-telemetry` | `send-telemetry` | unknown |
| `skill` | `help` | `help` | read |
| `skill install` | `skill.install` | `skill.install` | write |
| `skill list` | `skill.list` | `skill.list` | read |
| `skill preview` | `skill.preview` | `skill.preview` | read |
| `skill publish` | `skill.publish` | `skill.publish` | write |
| `skill search` | `skill.search` | `skill.search` | read |
| `skill update` | `skill.update` | `skill.update` | write |
| `ssh-key` | `help` | `help` | read |
| `ssh-key add` | `ssh-key.add` | `ssh-key.add` | write |
| `ssh-key delete` | `ssh-key.delete` | `ssh-key.delete` | write |
| `ssh-key list` | `ssh-key.list` | `ssh-key.list` | read |
| `status` | `status` | `status` | read |
| `telemetry` | `help` | `help` | read |
| `variable` | `help` | `help` | read |
| `variable delete` | `variable.delete` | `variable.delete` | write |
| `variable get` | `variable.get` | `variable.get` | read |
| `variable list` | `variable.list` | `variable.list` | read |
| `variable set` | `variable.set` | `variable.set` | write |
| `version` | `help` | `help` | read |
| `workflow` | `help` | `help` | read |
| `workflow disable` | `workflow.disable` | `workflow.disable` | write |
| `workflow enable` | `workflow.enable` | `workflow.enable` | write |
| `workflow list` | `workflow.list` | `workflow.list` | read |
| `workflow run` | `workflow.run` | `workflow.run` | write |
| `workflow view` | `workflow.view` | `workflow.view` | read |

### L1 flag-driven action overrides

| Command/condition | Family | Action | Effect |
| --- | --- | --- | --- |
| `pr merge --auto` | `pr.merge` | `pr.merge.auto.enable` | write |
| `pr merge --disable-auto` (without `--auto`) | `pr.merge` | `pr.merge.auto.disable` | write |
| `pr merge --admin` (without either auto flag) | `pr.merge` | `pr.merge.direct` | write |
| `pr review --approve` | `pr.review` | `pr.review.approve` | write |
| `pr review --request-changes` (without approve) | `pr.review` | `pr.review.request-changes` | write |
| `pr review --comment` (without earlier review flags) | `pr.review` | `pr.review.comment` | write |
| `repo set-default --view` | `repo.set-default` | `repo.set-default.view` | read |
| `secret set --no-store` | `secret.set` | `secret.set.encrypt` | read |
| `issue develop --list` | `issue.develop` | `issue.develop.list` | read |
| Parsed help/version on a known, known-effect command | `help` | `help` | read |

Invalid combinations remain conservative intents, not predictions of gh's argument
validation. For example, native `pr ready --undo` retains `pr.ready`, and native
comment edit/delete flags retain their base comment action. Interactive `pr review`
retains `pr.review` with unresolved review event.

### Built-in aliases

Each listed spelling maps to the canonical command in the right column. Root
aliases also work when followed by canonical descendants (`cs ssh` becomes
`codespace ssh`); the table lists all 57 explicit alias keys.

| Alias spellings | Canonical command |
| --- | --- |
| `agent`, `agent-tasks`, `agents` | `agent-task` |
| `alias ls` | `alias list` |
| `at` | `attestation` |
| `cache ls` | `cache list` |
| `cs` | `codespace` |
| `codespace ls`, `cs ls` | `codespace list` |
| `config ls` | `config list` |
| `discussion ls` | `discussion list` |
| `ext`, `extensions` | `extension` |
| `ext ls`, `extension ls`, `extensions ls` | `extension list` |
| `ext uninstall`, `extension uninstall`, `extensions uninstall` | `extension remove` |
| `gist new` | `gist create` |
| `gist ls` | `gist list` |
| `gpg-key ls` | `gpg-key list` |
| `issue new` | `issue create` |
| `issue ls` | `issue list` |
| `label ls` | `label list` |
| `org ls` | `org list` |
| `co`, `pr co` | `pr checkout` |
| `pr new` | `pr create` |
| `pr ls` | `pr list` |
| `project ls` | `project list` |
| `release new` | `release create` |
| `release ls` | `release list` |
| `repo autolink new` | `repo autolink create` |
| `repo autolink ls` | `repo autolink list` |
| `repo new` | `repo create` |
| `repo deploy-key ls` | `repo deploy-key list` |
| `repo gitignore ls` | `repo gitignore list` |
| `repo license ls` | `repo license list` |
| `repo ls` | `repo list` |
| `rs` | `ruleset` |
| `rs ls`, `ruleset ls` | `ruleset list` |
| `run ls` | `run list` |
| `secret remove` | `secret delete` |
| `secret ls` | `secret list` |
| `skills` | `skill` |
| `skill add`, `skills add` | `skill install` |
| `skill ls`, `skills ls` | `skill list` |
| `skill show`, `skills show` | `skill preview` |
| `ssh-key ls` | `ssh-key list` |
| `variable remove` | `variable delete` |
| `variable ls` | `variable list` |
| `workflow ls` | `workflow list` |

### L1 limitations

No native file/body/stdin content, interactive choice, local git/API resolution,
complete Cobra positional-count validation, enum validation, or actual outcome is
inferred. Known leaves with unknown flags keep intent but have heuristic confidence;
unknown flag arity discards positional extraction and reports ambiguous
`target.selector`. Attached unknown long values can leave positionals usable. If
unknown arity obscures command discovery, the command itself remains unknown.
`--json` without a value is not reproduced as gh's field-discovery listing.
`secret get` is not a baseline built-in. Clone/fork `--` tails are `gitArguments`,
not the clone directory.

**Dispatcher limitation:** L2/L3 is selected only when alias-expanded argv starts
with `api`. A root-option-prefixed spelling such as `gh -R o/r api ...` does not
enter the shared API parser; it is analyzed as the L1 `api` entry with unknown
effect. Native root-option command discovery is not equivalent to API dispatch.

## L2: REST requests

[API argument parser](../internal/normalize/apiargs/apiargs.go),
[REST normalizer](../internal/normalize/rest/rest.go),
[endpoint parser](../internal/normalize/rest/endpoint.go), and
[parameter builder](../internal/normalize/rest/params.go) reproduce supported
baseline request construction before [typed route matching](../internal/normalize/rest/routes.go).

### Request and endpoint semantics

- Exactly one positional endpoint is required. `-X`/`--method` wins; otherwise any
  raw/typed field or `--input` infers POST, and no fields/input means GET. Headers,
  pagination, output formatting, cache, and previews do not change that inference.
- API parsing accepts long attached/separate values, short attached/separate values,
  bundled boolean shorthand, explicit boolean values, interspersed flags, and `--`.
  Recognized flags are `allow-escape-sequences`, `cache`, `field` (`-F`), `header`
  (`-H`), `help`, `hostname`, `include` (`-i`), `input`, `jq` (`-q`), `method` (`-X`),
  `paginate`, `preview` (`-p`), `raw-field` (`-f`), `silent`, `slurp`, `template` (`-t`),
  and `verbose`. `allow-escape-sequences` is accepted but classification-irrelevant.
  API unknown flags/missing values/invalid booleans are parse unknowns. `--help`
  returns L2 `help` / read when argument parsing reaches a help result.
- Relative host precedence is `--hostname`, then `GH_HOST`, otherwise deferred
  gh-config/git context. `GH_REPO` supplies repository placeholders, not the API
  request host. Absolute api.github.com/uploads.github.com canonicalize to github.com;
  api.<tenant>.ghe.com canonicalizes to the tenant host. Other absolute URLs with
  `/api/v3/` are treated as enterprise REST, stripping that prefix. Other absolute
  paths/hosts are opaque and unknown, even for GET.
- Exactly one optional leading slash is normalized. A relative `/api/v3/` is not
  stripped because gh appends its own API prefix. Path segment decoding preserves
  encoded-slash boundaries, so an encoded branch/path can contain `/` without
  manufacturing an owner/repository split. Source display path can contain decoded
  slashes; use `origin.route` for the actual matched template.
- Empty/trailing/double-slash paths, traversal/dot segments, invalid escapes,
  controls, backslashes, and fragments are rejected as malformed unknowns. No
  redirects or path-cleaning semantics are guessed.
- `{owner}`/`{repo}` and legacy `:owner`/`:repo` substitute from
  `[HOST/]OWNER/REPO` in `GH_REPO`; missing context stays unresolved.
  `{branch}`/`:branch` defer git context. Unknown brace names remain literal. Typed
  fields substitute placeholders after scalar conversion; raw fields/file contents
  do not. GH_REPO URL forms are unsupported in this REST placeholder builder.

### Body/query representation and refinements

Raw `-f` fields are strings; typed `-F` fields convert integers, true/false/null,
or injected `@file`/`@-` bytes to strings. Raw fields are processed before typed
fields regardless of argv ordering. Nested keys (`a[b]`, `a[]`, `a[][c]`) construct
maps/lists; valueless `key[]` creates an empty array. Duplicate scalar overrides or
incompatible map/array types are malformed rather than silently overwritten.

GET fields become wire query strings (nested maps flatten to leaf keys; arrays use
`[]`). Endpoint query values are decoded and repeated values become string arrays.
`--input` supplies a JSON-object body for analysis, with accompanying fields in the
query. Invalid/unavailable/non-object/trailing-document body data preserves the
matched route but leaves parameters unresolved. The combined canonical parameter
map prefers body over query on collision; non-target route captures then take
precedence over payload values. Specialization uses **only the body**:

| Base request/body condition | Family | Emitted action | Effect |
| --- | --- | --- | --- |
| PR PATCH `state=closed` / `state=open` | `pr.close` / `pr.reopen` | `pr.close` / `pr.reopen` | write |
| Issue PATCH `state=closed` / `state=open` | `issue.close` / `issue.reopen` | `issue.close` / `issue.reopen` | write |
| Repo PATCH typed/JSON `archived: true` | `repo.archive` | `repo.archive` | write |
| Review create/submit `event=APPROVE` | `pr.review` | `pr.review.approve` | write |
| Review create/submit `event=REQUEST_CHANGES` | `pr.review` | `pr.review.request-changes` | write |
| Review create/submit `event=COMMENT` | `pr.review` | `pr.review.comment` | write |
| Review create with absent event and fully available body | `pr.review` | `pr.review.pending` | write |

Unrecognized/null/unavailable review events retain create/submit with unresolved
`parameters.event`. A query `?state=closed` does not manufacture a close action;
raw string `archived=true` is not the boolean archive refinement. Repo creation
can defer authenticated owner/name. Body refs/SHAs, PR base/head, workflow dispatch
refs, and branch/ref route selectors populate targets where supported.

Unregistered GET/HEAD has family `api.read`, action `api.read.unknown`, effect read,
heuristic confidence, and mutation-free true by GitHub REST convention. Unregistered
POST/PUT/PATCH/DELETE/**OPTIONS/TRACE/CONNECT** conservatively has write effect,
family `unknown`, and `api.<method>.unknown`; nonstandard methods have unknown effect.
There are no explicit HEAD routes in the registry.

`X-HTTP-Method-Override` becomes unknown/heuristic, with `operation-ambiguous`,
regardless of header value. Only that header name is recorded. A header missing
`:` or malformed request-field construction is reported skipped, unknown effect,
with rejection evidence. Explicit non-GET `--paginate` or `--paginate --input` also
becomes skipped/malformed while preserving target and origin; detail records the
intended action. **Implicit POST from fields with `--paginate` can still write**:
gh validates pagination before method inference, so it remains a will-run write.

### L2 family/action table

All 206 registered method/path pairs follow. GET rows have read effect; every
listed non-GET row has write effect. Body refinements above can replace the base
family/action. Matching requires the entire decoded segment sequence and exact
method; literals outrank captures. PR/issue numbers and `*_id` captures require
positive decimal integers (workflow IDs also accept `.yml`/`.yaml` filenames).
`{*ref}`/`{*path}` are terminal slash-bearing captures, not general substring matches.

| Method | Route | Family | Base action |
| --- | --- | --- | --- |
| GET | `/repos/{owner}/{repo}/pulls` | `pr.read` | `pr.list` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}` | `pr.read` | `pr.view` |
| POST | `/repos/{owner}/{repo}/pulls` | `pr.create` | `pr.create` |
| PATCH | `/repos/{owner}/{repo}/pulls/{pull_number}` | `pr.edit` | `pr.edit` |
| PUT | `/repos/{owner}/{repo}/pulls/{pull_number}/merge` | `pr.merge` | `pr.merge.direct` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/merge` | `pr.read` | `pr.merge.status` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/commits` | `pr.read` | `pr.commits.list` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/files` | `pr.read` | `pr.files.list` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews` | `pr.read` | `pr.reviews.list` |
| POST | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews` | `pr.review` | `pr.review.create` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}` | `pr.read` | `pr.review.view` |
| PUT | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}` | `pr.review` | `pr.review.update` |
| DELETE | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}` | `pr.review` | `pr.review.delete` |
| POST | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}/events` | `pr.review` | `pr.review.submit` |
| PUT | `/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}/dismissals` | `pr.review` | `pr.review.dismiss` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers` | `pr.read` | `pr.reviewers.list` |
| POST | `/repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers` | `pr.reviewers` | `pr.reviewers.request` |
| DELETE | `/repos/{owner}/{repo}/pulls/{pull_number}/requested_reviewers` | `pr.reviewers` | `pr.reviewers.remove` |
| PUT | `/repos/{owner}/{repo}/pulls/{pull_number}/update-branch` | `pr.update-branch` | `pr.update-branch` |
| GET | `/repos/{owner}/{repo}/pulls/comments` | `pr.read` | `pr.comments.list` |
| GET | `/repos/{owner}/{repo}/pulls/comments/{comment_id}` | `pr.read` | `pr.comment.view` |
| PATCH | `/repos/{owner}/{repo}/pulls/comments/{comment_id}` | `pr.comment` | `pr.comment.update` |
| DELETE | `/repos/{owner}/{repo}/pulls/comments/{comment_id}` | `pr.comment` | `pr.comment.delete` |
| GET | `/repos/{owner}/{repo}/pulls/{pull_number}/comments` | `pr.read` | `pr.comments.list` |
| POST | `/repos/{owner}/{repo}/pulls/{pull_number}/comments` | `pr.comment` | `pr.comment.create` |
| POST | `/repos/{owner}/{repo}/pulls/{pull_number}/comments/{comment_id}/replies` | `pr.comment` | `pr.comment.reply` |
| GET | `/repos/{owner}/{repo}/issues` | `issue.read` | `issue.list` |
| POST | `/repos/{owner}/{repo}/issues` | `issue.create` | `issue.create` |
| GET | `/repos/{owner}/{repo}/issues/{issue_number}` | `issue.read` | `issue.view` |
| PATCH | `/repos/{owner}/{repo}/issues/{issue_number}` | `issue.edit` | `issue.edit` |
| PUT | `/repos/{owner}/{repo}/issues/{issue_number}/lock` | `issue.lock` | `issue.lock` |
| DELETE | `/repos/{owner}/{repo}/issues/{issue_number}/lock` | `issue.unlock` | `issue.unlock` |
| GET | `/repos/{owner}/{repo}/issues/comments` | `issue.read` | `issue.comments.list` |
| GET | `/repos/{owner}/{repo}/issues/comments/{comment_id}` | `issue.read` | `issue.comment.view` |
| PATCH | `/repos/{owner}/{repo}/issues/comments/{comment_id}` | `issue.comment` | `issue.comment.update` |
| DELETE | `/repos/{owner}/{repo}/issues/comments/{comment_id}` | `issue.comment` | `issue.comment.delete` |
| GET | `/repos/{owner}/{repo}/issues/{issue_number}/comments` | `issue.read` | `issue.comments.list` |
| POST | `/repos/{owner}/{repo}/issues/{issue_number}/comments` | `issue.comment` | `issue.comment.create` |
| GET | `/repos/{owner}/{repo}/issues/{issue_number}/labels` | `issue.read` | `issue.labels.list` |
| POST | `/repos/{owner}/{repo}/issues/{issue_number}/labels` | `issue.labels` | `issue.labels.add` |
| PUT | `/repos/{owner}/{repo}/issues/{issue_number}/labels` | `issue.labels` | `issue.labels.replace` |
| DELETE | `/repos/{owner}/{repo}/issues/{issue_number}/labels` | `issue.labels` | `issue.labels.clear` |
| DELETE | `/repos/{owner}/{repo}/issues/{issue_number}/labels/{name}` | `issue.labels` | `issue.labels.remove` |
| POST | `/repos/{owner}/{repo}/issues/{issue_number}/assignees` | `issue.assignees` | `issue.assignees.add` |
| DELETE | `/repos/{owner}/{repo}/issues/{issue_number}/assignees` | `issue.assignees` | `issue.assignees.remove` |
| GET | `/repos/{owner}/{repo}/labels` | `repo.read` | `repo.labels.list` |
| POST | `/repos/{owner}/{repo}/labels` | `repo.labels` | `repo.labels.create` |
| PATCH | `/repos/{owner}/{repo}/labels/{name}` | `repo.labels` | `repo.labels.update` |
| DELETE | `/repos/{owner}/{repo}/labels/{name}` | `repo.labels` | `repo.labels.delete` |
| GET | `/repos/{owner}/{repo}` | `repo.read` | `repo.view` |
| PATCH | `/repos/{owner}/{repo}` | `repo.edit` | `repo.edit` |
| DELETE | `/repos/{owner}/{repo}` | `repo.delete` | `repo.delete` |
| GET | `/user/repos` | `repo.read` | `repo.list` |
| POST | `/user/repos` | `repo.create` | `repo.create` |
| GET | `/orgs/{org}/repos` | `repo.read` | `repo.list` |
| POST | `/orgs/{org}/repos` | `repo.create` | `repo.create` |
| GET | `/users/{username}/repos` | `repo.read` | `repo.list` |
| POST | `/repos/{owner}/{repo}/transfer` | `repo.transfer` | `repo.transfer` |
| POST | `/repos/{owner}/{repo}/forks` | `repo.fork` | `repo.fork` |
| GET | `/repos/{owner}/{repo}/forks` | `repo.read` | `repo.forks.list` |
| GET | `/repos/{owner}/{repo}/collaborators` | `repo.read` | `repo.collaborators.list` |
| PUT | `/repos/{owner}/{repo}/collaborators/{username}` | `repo.collaborators` | `repo.collaborators.add` |
| DELETE | `/repos/{owner}/{repo}/collaborators/{username}` | `repo.collaborators` | `repo.collaborators.remove` |
| POST | `/repos/{owner}/{repo}/git/refs` | `ref.create` | `ref.create` |
| GET | `/repos/{owner}/{repo}/git/ref/{*ref}` | `ref.read` | `ref.view` |
| GET | `/repos/{owner}/{repo}/git/matching-refs/{*ref}` | `ref.read` | `ref.list` |
| PATCH | `/repos/{owner}/{repo}/git/refs/{*ref}` | `ref.update` | `ref.update` |
| DELETE | `/repos/{owner}/{repo}/git/refs/{*ref}` | `ref.delete` | `ref.delete` |
| GET | `/repos/{owner}/{repo}/branches` | `branch.read` | `branch.list` |
| GET | `/repos/{owner}/{repo}/branches/{branch}` | `branch.read` | `branch.view` |
| GET | `/repos/{owner}/{repo}/branches/{branch}/protection` | `branch.protection` | `branch.protection.view` |
| PUT | `/repos/{owner}/{repo}/branches/{branch}/protection` | `branch.protection` | `branch.protection.update` |
| DELETE | `/repos/{owner}/{repo}/branches/{branch}/protection` | `branch.protection` | `branch.protection.delete` |
| POST | `/repos/{owner}/{repo}/branches/{branch}/protection/enforce_admins` | `branch.protection` | `branch.protection.admins.enable` |
| DELETE | `/repos/{owner}/{repo}/branches/{branch}/protection/enforce_admins` | `branch.protection` | `branch.protection.admins.disable` |
| PATCH | `/repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews` | `branch.protection` | `branch.protection.reviews.update` |
| DELETE | `/repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews` | `branch.protection` | `branch.protection.reviews.delete` |
| PATCH | `/repos/{owner}/{repo}/branches/{branch}/protection/required_status_checks` | `branch.protection` | `branch.protection.checks.update` |
| DELETE | `/repos/{owner}/{repo}/branches/{branch}/protection/required_status_checks` | `branch.protection` | `branch.protection.checks.delete` |
| GET | `/repos/{owner}/{repo}/rulesets` | `repo.read` | `repo.rulesets.list` |
| POST | `/repos/{owner}/{repo}/rulesets` | `repo.rulesets` | `repo.rulesets.create` |
| GET | `/repos/{owner}/{repo}/rulesets/{ruleset_id}` | `repo.read` | `repo.rulesets.view` |
| PUT | `/repos/{owner}/{repo}/rulesets/{ruleset_id}` | `repo.rulesets` | `repo.rulesets.update` |
| DELETE | `/repos/{owner}/{repo}/rulesets/{ruleset_id}` | `repo.rulesets` | `repo.rulesets.delete` |
| GET | `/orgs/{org}/rulesets` | `org.read` | `org.rulesets.list` |
| POST | `/orgs/{org}/rulesets` | `org.rulesets` | `org.rulesets.create` |
| PUT | `/orgs/{org}/rulesets/{ruleset_id}` | `org.rulesets` | `org.rulesets.update` |
| DELETE | `/orgs/{org}/rulesets/{ruleset_id}` | `org.rulesets` | `org.rulesets.delete` |
| GET | `/repos/{owner}/{repo}/contents` | `file.read` | `file.list` |
| GET | `/repos/{owner}/{repo}/contents/{*path}` | `file.read` | `file.view` |
| PUT | `/repos/{owner}/{repo}/contents/{*path}` | `file.put` | `file.put` |
| DELETE | `/repos/{owner}/{repo}/contents/{*path}` | `file.delete` | `file.delete` |
| GET | `/repos/{owner}/{repo}/releases` | `release.read` | `release.list` |
| POST | `/repos/{owner}/{repo}/releases` | `release.create` | `release.create` |
| GET | `/repos/{owner}/{repo}/releases/latest` | `release.read` | `release.latest` |
| GET | `/repos/{owner}/{repo}/releases/tags/{tag}` | `release.read` | `release.tag.view` |
| GET | `/repos/{owner}/{repo}/releases/{release_id}` | `release.read` | `release.view` |
| PATCH | `/repos/{owner}/{repo}/releases/{release_id}` | `release.update` | `release.update` |
| DELETE | `/repos/{owner}/{repo}/releases/{release_id}` | `release.delete` | `release.delete` |
| GET | `/repos/{owner}/{repo}/releases/{release_id}/assets` | `release.read` | `release.assets.list` |
| POST | `/repos/{owner}/{repo}/releases/{release_id}/assets` | `release.asset` | `release.asset.upload` |
| GET | `/repos/{owner}/{repo}/releases/assets/{asset_id}` | `release.read` | `release.asset.view` |
| PATCH | `/repos/{owner}/{repo}/releases/assets/{asset_id}` | `release.asset` | `release.asset.update` |
| DELETE | `/repos/{owner}/{repo}/releases/assets/{asset_id}` | `release.asset` | `release.asset.delete` |
| GET | `/repos/{owner}/{repo}/actions/workflows` | `workflow.read` | `workflow.list` |
| GET | `/repos/{owner}/{repo}/actions/workflows/{workflow_id}` | `workflow.read` | `workflow.view` |
| POST | `/repos/{owner}/{repo}/actions/workflows/{workflow_id}/dispatches` | `workflow.dispatch` | `workflow.dispatch` |
| PUT | `/repos/{owner}/{repo}/actions/workflows/{workflow_id}/enable` | `workflow.enable` | `workflow.enable` |
| PUT | `/repos/{owner}/{repo}/actions/workflows/{workflow_id}/disable` | `workflow.disable` | `workflow.disable` |
| GET | `/repos/{owner}/{repo}/actions/runs` | `run.read` | `run.list` |
| GET | `/repos/{owner}/{repo}/actions/runs/{run_id}` | `run.read` | `run.view` |
| POST | `/repos/{owner}/{repo}/actions/runs/{run_id}/rerun` | `run.rerun` | `run.rerun` |
| POST | `/repos/{owner}/{repo}/actions/runs/{run_id}/rerun-failed-jobs` | `run.rerun` | `run.rerun.failed` |
| POST | `/repos/{owner}/{repo}/actions/runs/{run_id}/cancel` | `run.cancel` | `run.cancel` |
| POST | `/repos/{owner}/{repo}/actions/runs/{run_id}/force-cancel` | `run.cancel` | `run.cancel.force` |
| DELETE | `/repos/{owner}/{repo}/actions/runs/{run_id}` | `run.delete` | `run.delete` |
| DELETE | `/repos/{owner}/{repo}/actions/runs/{run_id}/logs` | `run.logs` | `run.logs.delete` |
| POST | `/repos/{owner}/{repo}/actions/jobs/{job_id}/rerun` | `run.rerun` | `run.job.rerun` |
| GET | `/repos/{owner}/{repo}/actions/secrets` | `secret.read` | `secret.list` |
| PUT | `/repos/{owner}/{repo}/actions/secrets/{secret_name}` | `secret.put` | `secret.put` |
| DELETE | `/repos/{owner}/{repo}/actions/secrets/{secret_name}` | `secret.delete` | `secret.delete` |
| GET | `/orgs/{org}/actions/secrets` | `secret.read` | `secret.list` |
| PUT | `/orgs/{org}/actions/secrets/{secret_name}` | `secret.put` | `secret.put` |
| DELETE | `/orgs/{org}/actions/secrets/{secret_name}` | `secret.delete` | `secret.delete` |
| PUT | `/orgs/{org}/actions/secrets/{secret_name}/repositories` | `secret.repositories` | `secret.repositories.replace` |
| PUT | `/orgs/{org}/actions/secrets/{secret_name}/repositories/{repository_id}` | `secret.repositories` | `secret.repositories.add` |
| DELETE | `/orgs/{org}/actions/secrets/{secret_name}/repositories/{repository_id}` | `secret.repositories` | `secret.repositories.remove` |
| PUT | `/repos/{owner}/{repo}/environments/{environment}/secrets/{secret_name}` | `secret.put` | `secret.put` |
| DELETE | `/repos/{owner}/{repo}/environments/{environment}/secrets/{secret_name}` | `secret.delete` | `secret.delete` |
| GET | `/repos/{owner}/{repo}/actions/variables` | `variable.read` | `variable.list` |
| POST | `/repos/{owner}/{repo}/actions/variables` | `variable.create` | `variable.create` |
| PATCH | `/repos/{owner}/{repo}/actions/variables/{name}` | `variable.update` | `variable.update` |
| DELETE | `/repos/{owner}/{repo}/actions/variables/{name}` | `variable.delete` | `variable.delete` |
| POST | `/orgs/{org}/actions/variables` | `variable.create` | `variable.create` |
| PATCH | `/orgs/{org}/actions/variables/{name}` | `variable.update` | `variable.update` |
| DELETE | `/orgs/{org}/actions/variables/{name}` | `variable.delete` | `variable.delete` |
| PUT | `/orgs/{org}/actions/variables/{name}/repositories` | `variable.repositories` | `variable.repositories.replace` |
| PUT | `/orgs/{org}/actions/variables/{name}/repositories/{repository_id}` | `variable.repositories` | `variable.repositories.add` |
| DELETE | `/orgs/{org}/actions/variables/{name}/repositories/{repository_id}` | `variable.repositories` | `variable.repositories.remove` |
| POST | `/repos/{owner}/{repo}/environments/{environment}/variables` | `variable.create` | `variable.create` |
| PATCH | `/repos/{owner}/{repo}/environments/{environment}/variables/{name}` | `variable.update` | `variable.update` |
| DELETE | `/repos/{owner}/{repo}/environments/{environment}/variables/{name}` | `variable.delete` | `variable.delete` |
| GET | `/repos/{owner}/{repo}/hooks` | `hook.read` | `hook.list` |
| POST | `/repos/{owner}/{repo}/hooks` | `hook.create` | `hook.create` |
| PATCH | `/repos/{owner}/{repo}/hooks/{hook_id}` | `hook.update` | `hook.update` |
| DELETE | `/repos/{owner}/{repo}/hooks/{hook_id}` | `hook.delete` | `hook.delete` |
| POST | `/repos/{owner}/{repo}/hooks/{hook_id}/pings` | `hook.ping` | `hook.ping` |
| POST | `/repos/{owner}/{repo}/hooks/{hook_id}/tests` | `hook.test` | `hook.test` |
| POST | `/orgs/{org}/hooks` | `hook.create` | `hook.create` |
| PATCH | `/orgs/{org}/hooks/{hook_id}` | `hook.update` | `hook.update` |
| DELETE | `/orgs/{org}/hooks/{hook_id}` | `hook.delete` | `hook.delete` |
| GET | `/repos/{owner}/{repo}/keys` | `key.read` | `repo.keys.list` |
| POST | `/repos/{owner}/{repo}/keys` | `key.create` | `repo.keys.create` |
| DELETE | `/repos/{owner}/{repo}/keys/{key_id}` | `key.delete` | `repo.keys.delete` |
| GET | `/user/keys` | `key.read` | `user.keys.list` |
| POST | `/user/keys` | `key.create` | `user.keys.create` |
| DELETE | `/user/keys/{key_id}` | `key.delete` | `user.keys.delete` |
| POST | `/user/gpg_keys` | `key.create` | `user.gpg_keys.create` |
| DELETE | `/user/gpg_keys/{key_id}` | `key.delete` | `user.gpg_keys.delete` |
| POST | `/user/ssh_signing_keys` | `key.create` | `user.ssh_signing_keys.create` |
| DELETE | `/user/ssh_signing_keys/{key_id}` | `key.delete` | `user.ssh_signing_keys.delete` |
| GET | `/gists` | `gist.read` | `gist.list` |
| POST | `/gists` | `gist.create` | `gist.create` |
| GET | `/gists/{gist}` | `gist.read` | `gist.view` |
| PATCH | `/gists/{gist}` | `gist.update` | `gist.update` |
| DELETE | `/gists/{gist}` | `gist.delete` | `gist.delete` |
| PUT | `/gists/{gist}/star` | `gist.star` | `gist.star` |
| DELETE | `/gists/{gist}/star` | `gist.star` | `gist.unstar` |
| POST | `/gists/{gist}/forks` | `gist.fork` | `gist.fork` |
| POST | `/gists/{gist}/comments` | `gist.comment` | `gist.comment.create` |
| PATCH | `/gists/{gist}/comments/{comment_id}` | `gist.comment` | `gist.comment.update` |
| DELETE | `/gists/{gist}/comments/{comment_id}` | `gist.comment` | `gist.comment.delete` |
| GET | `/orgs/{org}/members` | `org.read` | `org.members.list` |
| DELETE | `/orgs/{org}/members/{username}` | `org.members` | `org.members.remove` |
| PUT | `/orgs/{org}/memberships/{username}` | `org.members` | `org.membership.put` |
| DELETE | `/orgs/{org}/memberships/{username}` | `org.members` | `org.membership.delete` |
| PUT | `/orgs/{org}/public_members/{username}` | `org.members` | `org.membership.publicize` |
| DELETE | `/orgs/{org}/public_members/{username}` | `org.members` | `org.membership.conceal` |
| PATCH | `/orgs/{org}` | `org.update` | `org.update` |
| GET | `/orgs/{org}/teams` | `team.read` | `team.list` |
| POST | `/orgs/{org}/teams` | `team.create` | `team.create` |
| PATCH | `/orgs/{org}/teams/{team_slug}` | `team.update` | `team.update` |
| DELETE | `/orgs/{org}/teams/{team_slug}` | `team.delete` | `team.delete` |
| PUT | `/orgs/{org}/teams/{team_slug}/memberships/{username}` | `team.members` | `team.membership.put` |
| DELETE | `/orgs/{org}/teams/{team_slug}/memberships/{username}` | `team.members` | `team.membership.delete` |
| PUT | `/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}` | `team.repositories` | `team.repository.put` |
| DELETE | `/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}` | `team.repositories` | `team.repository.delete` |
| POST | `/repos/{owner}/{repo}/merges` | `branch.merge` | `branch.merge` |
| POST | `/repos/{owner}/{repo}/check-runs` | `check.create` | `check.create` |
| PATCH | `/repos/{owner}/{repo}/check-runs/{check_id}` | `check.update` | `check.update` |
| POST | `/repos/{owner}/{repo}/statuses/{sha}` | `status.create` | `status.create` |
| GET | `/repos/{owner}/{repo}/deployments` | `deployment.read` | `deployment.list` |
| POST | `/repos/{owner}/{repo}/deployments` | `deployment.create` | `deployment.create` |
| DELETE | `/repos/{owner}/{repo}/deployments/{deployment_id}` | `deployment.delete` | `deployment.delete` |
| POST | `/repos/{owner}/{repo}/deployments/{deployment_id}/statuses` | `deployment.status` | `deployment.status.create` |
| GET | `/repos/{owner}/{repo}/environments` | `environment.read` | `environment.list` |
| GET | `/repos/{owner}/{repo}/environments/{environment}` | `environment.read` | `environment.view` |
| PUT | `/repos/{owner}/{repo}/environments/{environment}` | `environment.put` | `environment.put` |
| DELETE | `/repos/{owner}/{repo}/environments/{environment}` | `environment.delete` | `environment.delete` |
| GET | `/user` | `user.read` | `user.view` |
| PATCH | `/user` | `user.update` | `user.update` |
| GET | `/notifications` | `notification.read` | `notification.list` |
| PUT | `/notifications` | `notification.mark_read` | `notification.mark_read` |
| PUT | `/repos/{owner}/{repo}/notifications` | `notification.mark_read` | `notification.mark_read` |
| PATCH | `/notifications/threads/{thread_id}` | `notification.mark_read` | `notification.mark_read` |
| DELETE | `/notifications/threads/{thread_id}` | `notification.done` | `notification.done` |

### L2 limitations

This is an explicit registry, not complete OpenAPI coverage. Unregistered codespace
routes, GHES-version availability, auth/validation outcomes, redirects, configured
API remapping, and server side effects are not inferred. Host values describe
canonical instance identity, not proof of transport destination. An unavailable
body can prevent a refinement without hiding the underlying known write.
`{branch}` is retained as deferred intent even where upstream rejects it with
GH_REPO set. The combined parameter view loses the query value on a body collision.
JSON numbers use Go JSON decoding (`float64` internally); typed fields use `int`.

## L3: GraphQL AST

[Request reconstruction](../internal/normalize/graphql/request.go),
[AST analysis](../internal/normalize/graphql/analysis.go), and
[normalizer](../internal/normalize/graphql/graphql.go) use gqlparser/v2 parsing,
not regex extraction. The [semantic registry](../internal/normalize/graphql/registry.go)
has 39 mapped mutations; the [schema snapshot](../internal/normalize/graphql/schema_mutations.go)
contains all 278 public mutation names captured on 2026-10-14. The snapshot does
**not** provide full schema validation or semantic mappings for all those fields.

### Transport, selection, and execution

- Literal endpoint `graphql` groups non-GET fields other than `query` and
  `operationName` as variables. Absolute GraphQL URLs, `/graphql`, and `graphql/`
  do not apply this grouping; explicit nested `variables[...]` can provide body
  variables for non-literal non-GET requests. GET fields are URL parameters and
  are not grouped. Classification describes the document, not GitHub acceptance
  of that transport.
- `--input` is the request JSON body: it must be an object with a nonempty string
  `query`, optional string `operationName`, and optional object `variables`.
  Field flags become URL query parameters, not body variables. Batched arrays are
  unsupported. Null variables/operationName are treated as absent by reconstruction.
- Absolute URL host wins over `--hostname`, then `GH_HOST`, otherwise host is
  unresolved. Instance hosts are lowercased; api.github.com and tenancy API hosts
  canonicalize; ports 80/443 are removed, custom ports retained. Node IDs do not
  prove repository identity. Every analyzed mutation receives deferred repository
  lookup, even skipped/non-selected fields.
- Literal `graphql` has GHES GraphQL routing. Non-literal relative `/graphql` or
  `graphql/` is unknown-endpoint on a known GHES host because gh routes it under
  `/api/v3/`; an unresolved host gets an uncertainty detail. Absolute URL detection
  recognizes `/graphql` and `/api/graphql` paths (optional trailing slash).
- Named/anonymous operations, selected `operationName`, inline arguments,
  variables/defaults, nested objects/lists, response aliases, fragments and inline
  fragments are analyzed. Multiple operations require a selected name; duplicate
  operation/fragment names, missing/cyclic fragments, incompatible root types,
  duplicate resolved arguments/input fields, and invalid selection are unknown.
- Every root mutation field expanded through fragments is enumerated in document
  order, including repeated syntactic selections; response fields nested under
  a mutation are not additional mutations. There is no root-field merge/deduplication.
- Non-selected mutations remain visible with execution `not-selected`. Literal or
  resolved boolean `@skip`/`@include` can prove `skipped`; missing/nonboolean
  conditions are `maybe`. Evidence describes the condition. Unknown directives
  are noted and ignored for execution, not interpreted as arbitrary runtime effects.
- Missing variables are unresolved; explicitly supplied unavailable values do not
  silently fall back to defaults. Selected queries (including introspection) emit
  one `graphql.query` read. Subscriptions emit `graphql.subscription`, unknown
  effect, malformed-request; no support is claimed.

Input/document/file bounds are 1 MiB; syntactic and fragment-expansion depth is
256; expanded selections are limited to 10,000 per walker. These limits return
unknown, not mutation-free proof. Only the first 64 KiB of source text is retained.

### L3 mapped family/action table

Every mapped mutation has write effect. `Node ID input` identifies the syntactic
input copied into `target.nodeId`, not a guarantee of a resolved repository.
All original input fields are retained in parameters; canonical merge/review
refinements follow the table.

| Schema field | Family | Base action | Node ID input |
| --- | --- | --- | --- |
| `mergePullRequest` | `pr.merge` | `pr.merge.direct` | `pullRequestId` |
| `enablePullRequestAutoMerge` | `pr.merge` | `pr.merge.auto.enable` | `pullRequestId` |
| `disablePullRequestAutoMerge` | `pr.merge` | `pr.merge.auto.disable` | `pullRequestId` |
| `enqueuePullRequest` | `pr.merge` | `pr.merge.queue.enqueue` | `pullRequestId` |
| `dequeuePullRequest` | `pr.merge` | `pr.merge.queue.dequeue` | `id` |
| `addPullRequestReview` | `pr.review` | `pr.review` | `pullRequestId` |
| `submitPullRequestReview` | `pr.review` | `pr.review` | `pullRequestId; otherwise pullRequestReviewId` |
| `createPullRequest` | `pr.create` | `pr.create` | `repositoryId` |
| `updatePullRequest` | `pr.edit` | `pr.edit` | `pullRequestId` |
| `closePullRequest` | `pr.close` | `pr.close` | `pullRequestId` |
| `reopenPullRequest` | `pr.reopen` | `pr.reopen` | `pullRequestId` |
| `deleteRef` | `ref.delete` | `ref.delete` | `refId` |
| `createRef` | `ref.create` | `ref.create` | `repositoryId` |
| `updateRef` | `ref.update` | `ref.update` | `refId` |
| `updateRefs` | `ref.update` | `ref.update.batch` | `repositoryId` |
| `mergeBranch` | `branch.merge` | `branch.merge` | `repositoryId` |
| `createIssue` | `issue.create` | `issue.create` | `repositoryId` |
| `closeIssue` | `issue.close` | `issue.close` | `issueId` |
| `reopenIssue` | `issue.reopen` | `issue.reopen` | `issueId` |
| `deleteIssue` | `issue.delete` | `issue.delete` | `issueId` |
| `addComment` | `comment.create` | `comment.create` | `subjectId` |
| `updateIssueComment` | `comment.edit` | `comment.edit` | `id` |
| `deleteIssueComment` | `comment.delete` | `comment.delete` | `id` |
| `archiveRepository` | `repo.archive` | `repo.archive` | `repositoryId` |
| `unarchiveRepository` | `repo.unarchive` | `repo.unarchive` | `repositoryId` |
| `updateRepository` | `repo.edit` | `repo.edit` | `repositoryId` |
| `createRepository` | `repo.create` | `repo.create` | none |
| `createBranchProtectionRule` | `branch.protection` | `branch.protection.create` | `repositoryId` |
| `updateBranchProtectionRule` | `branch.protection` | `branch.protection.edit` | `branchProtectionRuleId` |
| `deleteBranchProtectionRule` | `branch.protection` | `branch.protection.delete` | `branchProtectionRuleId` |
| `revertPullRequest` | `pr.revert` | `pr.revert` | `pullRequestId` |
| `convertPullRequestToDraft` | `pr.draft` | `pr.draft` | `pullRequestId` |
| `markPullRequestReadyForReview` | `pr.ready` | `pr.ready` | `pullRequestId` |
| `requestReviews` | `pr.review` | `pr.review.request` | `pullRequestId` |
| `dismissPullRequestReview` | `pr.review` | `pr.review.dismiss` | `pullRequestReviewId` |
| `deletePullRequestReview` | `pr.review` | `pr.review.delete` | `pullRequestReviewId` |
| `updatePullRequestBranch` | `pr.update-branch` | `pr.update-branch` | `pullRequestId` |
| `addStar` | `star` | `star.add` | `starrableId` |
| `removeStar` | `star` | `star.remove` | `starrableId` |

Review event `APPROVE`, `REQUEST_CHANGES`, or `COMMENT` specializes both review
mutations to `pr.review.approve`, `pr.review.request-changes`, or
`pr.review.comment`. `addPullRequestReview` with absent event on an available
object becomes `pr.review.pending`; other absent/invalid events remain unresolved
with base action `pr.review`. Unlike REST state PATCH specialization,
`updatePullRequest(state:CLOSED)` retains `pr.edit`, with a detail about CLOSED.

`createPullRequest` extracts base/head; `createRef` extracts ref/SHA; `updateRef`
extracts SHA; `mergeBranch` extracts base/head. `expectedHeadOid` supplies target
SHA when present. `updateRefs` keeps batch values in parameters rather than
emitting a separate operation per ref. Missing required extracted values are
recorded unresolved; GitHub schema/type/authorization validation is not performed.

### Schema-known but unmapped mutation fields

The remaining 239 snapshot entries all map to **family `unknown`**, action
**`graphql.mutation.<field>`**, effect **write**, and reason **`unknown-mutation`**.
Here `<field>` is replaced exactly by each listed schema name; aliases do not
change it. Detail distinguishes these known names ("known GitHub mutation without
semantic mapping") from names absent from the snapshot. They still have exact
AST identity on a successfully parsed document, not a recognized semantic family.

| Schema fields (each uses the mapping above) |
| --- |
| `abortQueuedMigrations`, `abortRepositoryMigration`, `acceptEnterpriseAdministratorInvitation`, `acceptEnterpriseMemberInvitation`, `acceptTopicSuggestion`, `accessUserNamespaceRepository`, `addAssigneesToAssignable` |
| `addBlockedBy`, `addCloseIssueReferences`, `addDiscussionComment`, `addDiscussionPollVote`, `addEnterpriseOrganizationMember`, `addEnterpriseSupportEntitlement`, `addLabelsToLabelable` |
| `addProjectCard`, `addProjectColumn`, `addProjectV2DraftIssue`, `addProjectV2ItemById`, `addPullRequestCreationCapBypassUsers`, `addPullRequestReviewComment`, `addPullRequestReviewThread` |
| `addPullRequestReviewThreadReply`, `addReaction`, `addRelatesTo`, `addSubIssue`, `addUpvote`, `addVerifiableDomain`, `applyPendingIssueSuggestions` |
| `approveDeployments`, `approveVerifiableDomain`, `archiveProjectV2Item`, `archivePullRequest`, `cancelEnterpriseAdminInvitation`, `cancelEnterpriseMemberInvitation`, `cancelSponsorship` |
| `changeUserStatus`, `clearLabelsFromLabelable`, `clearProjectV2ItemFieldValue`, `cloneProject`, `cloneTemplateRepository`, `closeDiscussion`, `convertProjectCardNoteToIssue` |
| `convertProjectV2DraftIssueItemToIssue`, `copyProjectV2`, `createAttributionInvitation`, `createCheckRun`, `createCheckSuite`, `createCommitOnBranch`, `createDeployment` |
| `createDeploymentStatus`, `createDiscussion`, `createEnterpriseOrganization`, `createEnvironment`, `createIpAllowListEntry`, `createIssueField`, `createIssueFieldValue` |
| `createIssueType`, `createLabel`, `createLinkedBranch`, `createMigrationSource`, `createProject`, `createProjectV2`, `createProjectV2Field` |
| `createProjectV2IssueField`, `createProjectV2StatusUpdate`, `createProjectV2View`, `createRepositoryCustomProperty`, `createRepositoryRuleset`, `createSponsorsListing`, `createSponsorsTier` |
| `createSponsorship`, `createSponsorships`, `createUserList`, `declineTopicSuggestion`, `deleteDeployment`, `deleteDiscussion`, `deleteDiscussionComment` |
| `deleteEnvironment`, `deleteIpAllowListEntry`, `deleteIssueField`, `deleteIssueFieldValue`, `deleteIssueType`, `deleteLabel`, `deleteLinkedBranch` |
| `deletePackageVersion`, `deleteProject`, `deleteProjectCard`, `deleteProjectColumn`, `deleteProjectV2`, `deleteProjectV2Field`, `deleteProjectV2Item` |
| `deleteProjectV2StatusUpdate`, `deleteProjectV2View`, `deleteProjectV2Workflow`, `deletePullRequestReviewComment`, `deleteRepositoryCustomProperty`, `deleteRepositoryRuleset`, `deleteUserList` |
| `deleteVerifiableDomain`, `dismissRepositoryVulnerabilityAlert`, `followOrganization`, `followUser`, `grantEnterpriseOrganizationsMigratorRole`, `grantMigratorRole`, `importProject` |
| `inviteEnterpriseAdmin`, `inviteEnterpriseMember`, `linkProjectV2ToRepository`, `linkProjectV2ToTeam`, `linkRepositoryToProject`, `lockLockable`, `markDiscussionCommentAsAnswer` |
| `markFileAsViewed`, `markProjectV2AsTemplate`, `minimizeComment`, `moveProjectCard`, `moveProjectColumn`, `pinEnvironment`, `pinIssue` |
| `pinIssueComment`, `promoteRepositoryCustomProperty`, `publishSponsorsTier`, `regenerateEnterpriseIdentityProviderRecoveryCodes`, `regenerateVerifiableDomainToken`, `rejectDeployments`, `rejectPendingIssueSuggestions` |
| `removeAssigneesFromAssignable`, `removeBlockedBy`, `removeCloseIssueReferences`, `removeEnterpriseAdmin`, `removeEnterpriseIdentityProvider`, `removeEnterpriseMember`, `removeEnterpriseOrganization` |
| `removeEnterpriseSupportEntitlement`, `removeLabelsFromLabelable`, `removeOutsideCollaborator`, `removePullRequestCreationCapBypassUsers`, `removeReaction`, `removeRelatesTo`, `removeSubIssue` |
| `removeUpvote`, `reopenDiscussion`, `reorderEnvironment`, `replaceActorsForAssignable`, `reprioritizeSubIssue`, `requestReviewsByLogin`, `rerequestCheckSuite` |
| `rerequestReviews`, `resolveReviewThread`, `retireSponsorsTier`, `revokeEnterpriseOrganizationsMigratorRole`, `revokeMigratorRole`, `setEnterpriseIdentityProvider`, `setIssueFieldValue` |
| `setOrganizationInteractionLimit`, `setRepositoryCustomPropertyValues`, `setRepositoryInteractionLimit`, `setUserInteractionLimit`, `startOrganizationMigration`, `startRepositoryMigration`, `transferEnterpriseOrganization` |
| `transferIssue`, `unarchiveProjectV2Item`, `unarchivePullRequest`, `unfollowOrganization`, `unfollowUser`, `unlinkProjectV2FromRepository`, `unlinkProjectV2FromTeam` |
| `unlinkRepositoryFromProject`, `unlockLockable`, `unmarkDiscussionCommentAsAnswer`, `unmarkFileAsViewed`, `unmarkIssueAsDuplicate`, `unmarkProjectV2AsTemplate`, `unminimizeComment` |
| `unpinIssue`, `unpinIssueComment`, `unresolveReviewThread`, `updateCheckRun`, `updateCheckSuitePreferences`, `updateDiscussion`, `updateDiscussionComment` |
| `updateEnterpriseAdministratorRole`, `updateEnterpriseAllowPrivateRepositoryForkingSetting`, `updateEnterpriseDefaultRepositoryPermissionSetting`, `updateEnterpriseDeployKeySetting`, `updateEnterpriseMembersCanChangeRepositoryVisibilitySetting`, `updateEnterpriseMembersCanCreateRepositoriesSetting`, `updateEnterpriseMembersCanDeleteIssuesSetting` |
| `updateEnterpriseMembersCanDeleteRepositoriesSetting`, `updateEnterpriseMembersCanInviteCollaboratorsSetting`, `updateEnterpriseMembersCanMakePurchasesSetting`, `updateEnterpriseMembersCanUpdateProtectedBranchesSetting`, `updateEnterpriseMembersCanViewDependencyInsightsSetting`, `updateEnterpriseOrganizationProjectsSetting`, `updateEnterpriseOwnerOrganizationRole` |
| `updateEnterpriseProfile`, `updateEnterpriseRepositoryProjectsSetting`, `updateEnterpriseTwoFactorAuthenticationDisallowedMethodsSetting`, `updateEnterpriseTwoFactorAuthenticationRequiredSetting`, `updateEnvironment`, `updateIpAllowListEnabledSetting`, `updateIpAllowListEntry` |
| `updateIpAllowListForInstalledAppsEnabledSetting`, `updateIpAllowListUserLevelEnforcementEnabledSetting`, `updateIssue`, `updateIssueField`, `updateIssueFieldValue`, `updateIssueIssueType`, `updateIssueType` |
| `updateLabel`, `updateNotificationRestrictionSetting`, `updateOrganizationAllowPrivateRepositoryForkingSetting`, `updateOrganizationWebCommitSignoffSetting`, `updatePatreonSponsorability`, `updateProject`, `updateProjectCard` |
| `updateProjectColumn`, `updateProjectV2`, `updateProjectV2Collaborators`, `updateProjectV2DraftIssue`, `updateProjectV2Field`, `updateProjectV2ItemFieldValue`, `updateProjectV2ItemPosition` |
| `updateProjectV2StatusUpdate`, `updateProjectV2View`, `updatePullRequestReview`, `updatePullRequestReviewComment`, `updateRepositoryCustomProperty`, `updateRepositoryRuleset`, `updateRepositoryWebCommitSignoffSetting` |
| `updateSponsorshipPreferences`, `updateSubscription`, `updateTeamReviewAssignment`, `updateTeamsRepository`, `updateTopics`, `updateUserList`, `updateUserListsForItem` |
| `verifyVerifiableDomain` |

A field absent from the snapshot uses the same unknown/write mapping with detail
`not in schema snapshot (2026-10-14)`. For example, `deleteRepository` is absent
from this public snapshot and is not a supported semantic mapping.

### L3 limitations

AST identity and syntax are supported; full GitHub schema validation, server
acceptance, authorization, operation effects, authenticated context, and node-ID
repository inference are not. Embedded snapshot names can differ from a GHES
schema or later public schema. Unknown fields remain explicit writes even if their
name resembles a read. Unknown directives and duplicate/overlapping field
execution are not simulated beyond the documented selection traversal. Deferred
repository/variable/input gaps are not necessarily resolvable by context lookup.

## Stdin and file analysis

The normalizer has no ambient stdin/filesystem/network/environment reads. Its
`Invocation` supplies argv, environment map, cwd, aliases, captured `StdinData`,
and an optional bounded `ReadFile` callback. "Pure" here means I/O is injected,
not that analyzing an explicitly provided file cannot call that reader. L1 does
not call it; L2/L3 can. Missing data remains unresolved/unknown.

The wrapper/inspection CLI checks alias-expanded API arguments for `--input -`
or a typed `-F key=@-`. It does not pre-read native interactive input such as
`auth login --with-token`, `secret set`, or `workflow run --json`; API parse errors
also skip this pre-read. Raw `-f query=@-` is a literal string, not a stdin request.

| Stdin status | Analysis behavior |
| --- | --- |
| `not-captured` | No capture requested/attempted; a requested but absent value is unavailable. |
| `captured` | Entire captured byte sequence available for analysis. |
| `tty` | Terminal deliberately untouched; no interactive pre-read. |
| `too-large` | No analysis bytes; explicit bound exceeded. |
| `timeout` | No complete analysis bytes; producer did not finish before deadline. |
| `error` | Read error; diagnostic in the injected stdin error field. |

Runtime defaults are `stdinMaxBytes: 8388608` (8 MiB), `stdinTimeout: 30s`, and
`fileMaxBytes: 8388608`. GraphQL's internal **1 MiB** bound still applies even if
these runtime limits are raised. REST has no additional equivalent document cap;
it uses the caller's bounded reader/capture.

For wrapper execution, regular-file input is inspected from its current offset
and restored. Streams use a private, immediately unlinked temporary file. Complete
capture can be replayed from that file; timeout/over-limit capture preserves the
consumed prefix and supervises a pump of prefix plus remaining live input. Bytes
and original argv are not rewritten; if consumed bytes cannot be preserved, real
gh is not started. Analysis spooling can change a pipe's descriptor type to a
regular file, and concurrently modified regular files are not snapshotted. See
[process notes](notes/process.md#stdin) for replay/ownership details.

REST models consumption order: the first typed `@-` receives captured stdin,
subsequent typed `@-` fields get empty strings, and `--input -` after a field has
consumed stdin cannot recover that body. **GraphQL multiple simultaneous stdin
consumers are not accurately emulated:** its shared parameter builder and body
reader each see the same captured bytes. Prefer exactly one stdin consumer when
relying on GraphQL analysis; do not assume that several `@-` fields and/or
`--input -` model sequential gh reads. Real gh still receives byte-exact replay.

Unavailable REST input preserves known route intent with unresolved parameters.
Unavailable GraphQL query/body/operation-name input becomes unknown; unavailable
variables remain unresolved. Timeout/TTY is input-unavailable for GraphQL, and
size/depth/selection bounds are input-too-large. How an unknown is allowed,
denied, or observed is a policy decision, not an automatic safety assertion by
normalization.

## Coverage summary and implementation references

| Area | Supported | Partial / deliberately not inferred | Unsupported / unknown |
| --- | --- | --- | --- |
| Native | All 243 static baseline registry paths and 57 built-in alias spellings; registered flag/selector extraction; plain alias chains. | Context lookup, interactive values, body contents, runtime effects, enum/complete Cobra validation; heuristic unknown flags on known leaves. | Unregistered/newer commands, external extension effects, shell aliases, alias failures, several runtime integrations. |
| REST | All 206 listed typed method/route pairs; supported API argv/body/query construction, host/path encoding and body-driven refinements. | Unavailable bodies/context, combined query/body collisions, gh-config transport remapping, GHES version/validation/outcome. | Complete OpenAPI coverage, redirects, unusual/malformed paths, arbitrary absolute URL semantics; writes on unregistered routes retain unknown family. |
| GraphQL | AST selection/variables/aliases/fragments/directives; 39 detailed mappings and explicit unknown writes for all other fields. | Unavailable variables/node lookup, schema/version drift, nonstandard transport acceptance, execution beyond syntactic selection. | Full schema/type/authorization validation, batching/subscriptions, multiple stdin-consumer fidelity; schema-known unmapped fields are not semantic support. |

Normalization is extensible by editing the existing static registries; it does
not load runtime normalizer plugins. Context resolution is a separate layer and
never turns normalization intent into a post-execution side-effect confirmation.
Implementation notes retain design/regeneration/evidence details:
[native](notes/native.md), [REST](notes/rest.md), [GraphQL](notes/graphql.md), and
[context resolution](notes/resolve.md).
