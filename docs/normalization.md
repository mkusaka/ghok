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

Both commands redact output by default and resolve local git metadata plus
`GH_REPO`/`GH_HOST` without network access, as does `hooks test`. `--no-resolve`
disables local lookup, not explicit targets or the normalizer's environment
inputs. `--allow-api` permits read-only API resolution without `--resolve` and
cannot be combined with `--no-resolve`. The deprecated `--resolve` remains a
no-op for compatibility. Commands can still read explicitly supplied API files
or non-TTY stdin for analysis. Explain leads with the policy decision and hook
selection, then operations with each unresolved field/reason once; normalize
retains its JSON schema. The implementation's CLI/API baseline is **gh 2.102.0**,
not an assertion of coverage for every future command, REST route, or GitHub
Enterprise Server version.

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
| `texts` | Optional normalized `title`, `body`, and `notes` prose with provenance; complements rather than replaces parameters. |
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

JSONL logging additionally minimizes ordinary content; hook/normalize/explain
copies retain it. Prose fields and whitespace-containing/over-64-byte values
are summarized, structural targets remain readable, and GraphQL source becomes
a byte-count/SHA256 fingerprint. Deliberately unexamined stdin can be logged as
`allow` while its operation remains unknown; this does not prove mutation-free.

### Normalized texts

`operations[].texts` exposes prose without requiring a hook to re-parse argv,
REST fields, or GraphQL input. It is an additive, optional schemaVersion 1 field.
Each optional `title`, `body`, or `notes` is a Text object with required `value`,
`source`, and `bytes`, plus optional `path`, `status`, and `truncated`. `bytes`
counts original text bytes before output redaction, not Unicode characters or
the eventual server-side content. Missing fields mean no corresponding text
was supplied/identified. Explicit empty inline values remain present.

Sources are `inline`, `file`, `stdin`, `editor`, `fill`, `template`, or `unknown`.
Unavailable/generated prose has an empty value; a failed file/stdin read also
adds an unresolved `texts.*` entry. Native unavailable stdin carries `status`:
`not-captured`, `tty`, `too-large`, `timeout`, or `error`. Successful capture
omits status. Known size-limit failures set `truncated`; **secret-scanning hooks
should deny truncated input** rather than assume the empty value is complete.
Hooks needing complete inspection should also reject unavailable/generated text.

File reads use the injected bounded `Invocation.ReadFile`, normally limited by
`runtime.fileMaxBytes` (8 MiB by default). Native `--body-file -`, `-F -`, and
`--notes-file -` use the existing bounded stdin spool when an enabled pre-hook
could match. TTY stdin is never read. Disabled/unrelated hooks, logging, and
post-hooks alone do not trigger capture. The full original stream is replayed
byte-exactly to gh, even after over-limit or timed-out analysis.

`bytes` is the available value's size on successful reads. For truncated native
wrapper input, regular-file sizes are reported in full (remaining bytes from the
original offset for redirected stdin); a stream's total is unknown before execution.
Unavailable values with unknown sizes report zero bytes. Truncated values are
withheld, not silently presented as complete text.

| Layer/input | Normalized text |
| --- | --- |
| L1 PR/issue create/edit `--title`, `--body`, `--body-file` | `title`, `body`. |
| L1 PR/issue comment and PR review `--body`/`--body-file` | `body`. |
| L1 issue close/reopen (and corresponding PR commands) `--comment` | `body`, when supplied. |
| L1 PR merge `--subject`, `--body`, `--body-file` | Commit `title`, `body`. |
| L1 release create/edit `--title`, `--notes`, `--notes-file` | `title`, `notes`. |
| L2 known PR/issue/comment/review request bodies | String `title`/`body` mapped by route semantics. |
| L2 release request `name`, `body` | `title`, `notes`. |
| L2 PR merge `commit_title`, `commit_message` | Commit `title`, `body`. |
| L2 review dismissal `message` | `body`. |
| L3 PR/issue/comment/review create/update inputs | String `title`/`body`, when applicable. |
| L3 merge/auto-merge `commitHeadline`, `commitBody` | Commit `title`, `body`. |
| L3 review dismissal `message` | `body`. |

L1 uses the existing pflag parse, so `-db "text"`, `--body=text`, and separate
`-b text` all expose the same body. Explicit text takes precedence over fill
defaults, but explicit editor/web flows report `editor` because initial text can
change. Commit-derived fill inputs and templates likewise are not final prose.
Generated/tag-derived release notes report `fill`, including when supplied notes
would only be a prefix. File paths retain their supplied spelling. No gist file
content is extracted.

L2 extracts from the actual reconstructed body, not URL query strings or
`--input` companion query fields. `-f body=...` is inline;
`-F body=@path` is file-backed; `@-` is stdin-backed; JSON `--input` values retain
the body's file/stdin provenance. No second file read is needed for texts.
GraphQL extracts semantic values after AST/variable resolution; its provenance
describes the input transport, not an editor or a server-generated message.

Hook/normalize/explain output retains ordinary prose while applying credential
redaction. JSONL text values become `[content: N bytes]`, where N counts the
available value before redaction; original size/provenance/status metadata is
preserved. Existing `parameters.body`, `parameters.bodyFile`, and other flags
retain their previous representations.
For example, `gh pr create -t T -b B` adds:

```json
{"texts":{"title":{"value":"T","source":"inline","bytes":1},"body":{"value":"B","source":"inline","bytes":1}}}
```

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
| `unknown-mutation` | GraphQL root mutation is absent from the embedded schema snapshot. It remains an unknown-family write; schema-known mutations without curated overrides are classified writes. |
| `parse-error` | Argument/alias lexer/JSON/GraphQL parse failure or recovered internal normalizer panic. API unknown flags also take this path. |
| `input-unavailable` | GraphQL query/body/operation name cannot be read, including TTY or timed-out stdin. REST unavailable bodies ordinarily preserve the route and add unresolved parameters instead. |
| `input-too-large` | GraphQL document/input/selection/depth bound exceeded, or recognizable input reader size-limit failure. |
| `operation-ambiguous` | Multiple GraphQL operations without selection; REST `X-HTTP-Method-Override` also uses this reason because server-side method is uncertain. |
| `malformed-request` | Structurally invalid input, unsupported subscription/batching, invalid path, invalid request field/header construction, or recognized rejected REST pagination combination. |

Unknown actions are not always just `unknown`: unmapped REST requests use
`api.<lowercase-method>.unknown`; snapshot-absent GraphQL fields use
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
| `gh pr merge 123 --admin --squash` | `pr.merge.direct` | Admin/direct intent bypasses requirements and the merge queue; the server may still reject it. Detail describes this intent, not server-selected direct/queue/auto behavior or confirmed success. |
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
[target extractors](../internal/normalize/native/targets.go),
[generated metadata](../internal/normalize/native/registry_gen.go), and
[curated semantics](../internal/normalize/native/semantics.go) cover 248 canonical
non-root commands plus root and 113 built-in alias paths. The real gh Cobra tree
includes groups, hidden commands/help topics, official extension install stubs,
and completion entry points. Identity alone does not prove known effects or targets.
Generated commands without curation are unknown family/action/effect with reason
`unknown-command` and detail `gh command not yet classified by ghok`; a curation
completeness test fails after regeneration until every command has been reviewed.
Existing actions stay stable; newly classified hidden commands use `native.*`.

Native flags use **pflag v1.0.10**, matching gh 2.102.0. FlagSets use generated
local/inherited metadata: bool, string, stringArray, stringSlice, int, uint16,
uint64, float64 and duration, with exact optional-value `NoOptDefVal` and
hidden/deprecation metadata. Invalid numeric/duration/CSV values retain command
intent but become heuristic, non-mutation-free parse errors, even with `--help`.
Supported forms include
interspersed flags, options before subcommands, `--flag=value`, `--flag value`,
`-Rvalue`, `-R=value`, `-R value`, bundled short booleans, explicit boolean values,
repeated flags, and `--`. Registered `-h` meanings take precedence over help
(auth hostname, config host, repo homepage).
Optional shorthand values require `=`: `browse -c=main` selects commit `main`,
but `browse -cn` selects commit `last` plus no-browser. An attached suffix without
`=` is a shorthand bundle, not an optional value.
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
- Flag parameter keys are lower camel case. Booleans are booleans; StringSlice
  options split CSV into arrays, while StringArray options retain literal commas
  (workflow fields). Other options remain pflag-normalized strings.
  Native bodies/body files/env files record presence,
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

`extension exec` and other DisableFlagParsing commands retain forwarded options as
arguments. Direct unknown commands may be extensions or newer gh commands; no extension
executable is analyzed. Copilot, codespace code/Jupyter/SSH integration and official
extension install stubs (`aw`, `stack`, `webhook`) have unknown effects even with exact
identity. Clone/checkout and auth/config/install operations are conservative writes;
downloads and codespace selection are GitHub reads although they can write local files.
This taxonomy is not local-filesystem isolation.

Regenerate with `go generate ./internal/normalize/native`. The development-only
`gen` module pins cli/cli **v2.102.0**, commit
`fc4b137cdef0a6bd28fd461b7cf9c84a5812a8cd`; the generated header records its module
sum. Generator dependencies and its Go 1.27.0 requirement do not enter ghok's root
module. Builds/tests stay offline and use the checked-in snapshot. For a new release,
bump `gen/go.mod`, tidy that module, regenerate, and fix
`TestRegistryClassifiesEveryCommand` in `semantics.go`; curate flag-driven overrides
in `extractParameters`, not flag metadata. Exact steps and review requirements are in
[native maintenance notes](notes/native.md#regenerating-registry_gengo-and-curating-overrides).

### L1 family/action table

The following rows give the default emitted action after native specialization.
`pr merge` has raw registry action `pr.merge`, but always specializes to
`pr.merge.request` or the flag-driven alternatives below. The empty root invocation
is a curated `help` / `help` / read. `co` and `__completeNoDesc` below share their
canonical commands' semantics rather than having duplicate curation.

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
| `attestation inspect` | `native.attestation.inspect` | `native.attestation.inspect` | read |
| `auth git-credential` | `native.auth.git-credential` | `native.auth.git-credential` | read |
| `codespace select` | `native.codespace.select` | `native.codespace.select` | read |
| `repo credits` | `native.repo.credits` | `native.repo.credits` | read |
| `aw` | `native.aw` | `native.aw` | unknown |
| `stack` | `native.stack` | `native.stack` | unknown |
| `webhook` | `native.webhook` | `native.webhook` | unknown |

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
aliases also work with all canonical and aliased descendants (`cs ssh` becomes
`codespace ssh`). The generator emits all 113 alias paths, including descendant
combinations; this table lists alias roots and changed leaf spellings, rather than
repeating every descendant.

| Alias spellings | Canonical command |
| --- | --- |
| `__completeNoDesc` | `__complete` |
| `a11y` | `accessibility` |
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

Native prose extraction is limited to the mapped text-bearing commands above.
Interactive choices, generated/final editor text, local git/API resolution,
complete Cobra positional-count validation, enum validation, and actual outcomes
are not inferred. Numeric/duration values use generated pflag types, but gh's custom string
enum validators are not reconstructed from Value.Type().
Known leaves with unknown flags keep intent but have heuristic confidence.
Unknown flag evidence comes from strict pflag errors; a fresh parse uses pflag's
unknown-flag allowlist, which consumes a following non-option for a bare unknown.
Unknown flag arity discards positional extraction and reports ambiguous
`target.selector`, even if help/version was also supplied. Attached unknown long
values can leave positionals usable. If unknown arity obscures command discovery,
the command itself remains unknown.
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

Routes are generated from GitHub's [public OpenAPI description](https://github.com/github/rest-api-description/blob/ad351b19702b757bfd03a8ee85bcafe717878331/descriptions/api.github.com/api.github.com.json),
commit `ad351b19702b757bfd03a8ee85bcafe717878331` (2026-10-13):
**1,242 operations**, **206 operationId overrides**, and **1,036 default namespaced
operations**. Generated metadata includes path-parameter types, tags, and
deprecation; matching a typed route gives exact confidence, not API validation.

Regenerate with `go generate ./internal/normalize/rest`. Offline:
`cd internal/normalize/rest && go run ./gen -spec /path/to/api.github.com.json -ref ad351b19702b757bfd03a8ee85bcafe717878331 -date 2026-10-13`.
Upgrade the SHA/date in `routes.go`'s directive, regenerate, and curate
`overrides.go` by exact operationId. Never edit `routes_gen.go`. Preserve existing
policy/hook names; `TestOverrideContract` detects absent keys, new action
collisions, and stable family/action changes. Its explicit legacy sharing groups
allow equivalent operations across scopes. Review any contract digest/allowlist
change. See [REST notes](notes/rest.md#generation-and-override-curation).

Without an override, action is `rest.` + operationId with `/` replaced by `.`,
family is `rest.` + its first segment, GET/HEAD is read, POST/PUT/PATCH/DELETE is
write, and other methods are unknown. Deprecated operations retain their method
effect. For example, codespace creation now emits exact
`rest.codespaces.create-for-authenticated-user` rather than an unknown write.
`repos/merge-upstream` uses `branch.merge` / `branch.merge-upstream`, not PR merge.

### Request and endpoint semantics

- Exactly one positional endpoint is required. `-X`/`--method` wins; otherwise any
  raw/typed field or `--input` infers POST, and no fields/input means GET. Headers,
  pagination, output formatting, cache, and previews do not change that inference.
- API parsing uses **pflag v1.0.10** with the flag declarations/types from
  [cli/cli 2.102.0 `pkg/cmd/api/api.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/api/api.go).
  Endpoint count uses Cobra's `ExactArgs(1)`. Long attached/separate values, short
  attached/separate values, bundled boolean shorthand, explicit boolean values,
  interspersed flags, and `--` follow pflag rather than a hand-written scanner.
  Recognized flags are `allow-escape-sequences`, `cache`, `field` (`-F`), `header`
  (`-H`), `help`, `hostname`, `include` (`-i`), `input`, `jq` (`-q`), `method` (`-X`),
  `paginate`, `preview` (`-p`), `raw-field` (`-f`), `silent`, `slurp`, `template` (`-t`),
  and `verbose`. `allow-escape-sequences` is accepted but classification-irrelevant.
  Fields and headers use string arrays (commas remain literal); previews use
  string slices with CSV quoting and repeated occurrences. Cache values must be
  valid Go durations. Explicit method selection uses `FlagSet.Changed("method")`,
  including an explicitly empty value, rather than comparing against GET.
  API unknown flags/missing values/invalid booleans, durations, or preview CSV
  are parse unknowns. `--help` and pflag's `-h` help shorthand return L2 `help` /
  read only when parsing reaches a help result; errors before help stay unknown.
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
- `/` is the documented API root (`rest.meta.root`); empty endpoints, non-root
  trailing/double-slash paths, traversal/dot segments, invalid escapes, controls,
  backslashes, and fragments are malformed unknowns. No redirects or path-cleaning
  semantics are guessed.
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

The canonical table below retains all 206 legacy route spellings plus upstream
branch merge, covering 206 operationId overrides (contents root and file view
share `repos/get-content`). Other snapshot routes use the defaults above.
Body refinements can replace these base family/actions. Integer **schema types**
require positive decimals without overflow; a name ending in `_id` does not make
a string numeric (for example `gist_id`). Workflow IDs also accept `.yml`/`.yaml`
filenames. Methods and the full decoded segment sequence must match; literals
outrank captures, including string captures. Only terminal contents paths and git
refs consume additional segments. The index uses method, first literal segment,
and segment count, with a separate terminal-capture bucket.

`origin.route` and non-target parameter keys now use the OpenAPI spellings:
`environment_name`, `gist_id`, `gpg_key_id`, `ssh_signing_key_id`, `check_run_id`.
Conventional owner/repo/org, PR/issue numbers, ref/branch, and SHA selectors
(`sha`, `commit_sha`, `file_sha`, `tag_sha`, `tree_sha`) populate targets.

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
| GET | `/repos/{owner}/{repo}/git/ref/{ref}` | `ref.read` | `ref.view` |
| GET | `/repos/{owner}/{repo}/git/matching-refs/{ref}` | `ref.read` | `ref.list` |
| PATCH | `/repos/{owner}/{repo}/git/refs/{ref}` | `ref.update` | `ref.update` |
| DELETE | `/repos/{owner}/{repo}/git/refs/{ref}` | `ref.delete` | `ref.delete` |
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
| GET | `/repos/{owner}/{repo}/contents/{path}` | `file.read` | `file.view` |
| PUT | `/repos/{owner}/{repo}/contents/{path}` | `file.put` | `file.put` |
| DELETE | `/repos/{owner}/{repo}/contents/{path}` | `file.delete` | `file.delete` |
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
| PUT | `/repos/{owner}/{repo}/environments/{environment_name}/secrets/{secret_name}` | `secret.put` | `secret.put` |
| DELETE | `/repos/{owner}/{repo}/environments/{environment_name}/secrets/{secret_name}` | `secret.delete` | `secret.delete` |
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
| POST | `/repos/{owner}/{repo}/environments/{environment_name}/variables` | `variable.create` | `variable.create` |
| PATCH | `/repos/{owner}/{repo}/environments/{environment_name}/variables/{name}` | `variable.update` | `variable.update` |
| DELETE | `/repos/{owner}/{repo}/environments/{environment_name}/variables/{name}` | `variable.delete` | `variable.delete` |
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
| DELETE | `/user/gpg_keys/{gpg_key_id}` | `key.delete` | `user.gpg_keys.delete` |
| POST | `/user/ssh_signing_keys` | `key.create` | `user.ssh_signing_keys.create` |
| DELETE | `/user/ssh_signing_keys/{ssh_signing_key_id}` | `key.delete` | `user.ssh_signing_keys.delete` |
| GET | `/gists` | `gist.read` | `gist.list` |
| POST | `/gists` | `gist.create` | `gist.create` |
| GET | `/gists/{gist_id}` | `gist.read` | `gist.view` |
| PATCH | `/gists/{gist_id}` | `gist.update` | `gist.update` |
| DELETE | `/gists/{gist_id}` | `gist.delete` | `gist.delete` |
| PUT | `/gists/{gist_id}/star` | `gist.star` | `gist.star` |
| DELETE | `/gists/{gist_id}/star` | `gist.star` | `gist.unstar` |
| POST | `/gists/{gist_id}/forks` | `gist.fork` | `gist.fork` |
| POST | `/gists/{gist_id}/comments` | `gist.comment` | `gist.comment.create` |
| PATCH | `/gists/{gist_id}/comments/{comment_id}` | `gist.comment` | `gist.comment.update` |
| DELETE | `/gists/{gist_id}/comments/{comment_id}` | `gist.comment` | `gist.comment.delete` |
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
| POST | `/repos/{owner}/{repo}/merge-upstream` | `branch.merge` | `branch.merge-upstream` |
| POST | `/repos/{owner}/{repo}/check-runs` | `check.create` | `check.create` |
| PATCH | `/repos/{owner}/{repo}/check-runs/{check_run_id}` | `check.update` | `check.update` |
| POST | `/repos/{owner}/{repo}/statuses/{sha}` | `status.create` | `status.create` |
| GET | `/repos/{owner}/{repo}/deployments` | `deployment.read` | `deployment.list` |
| POST | `/repos/{owner}/{repo}/deployments` | `deployment.create` | `deployment.create` |
| DELETE | `/repos/{owner}/{repo}/deployments/{deployment_id}` | `deployment.delete` | `deployment.delete` |
| POST | `/repos/{owner}/{repo}/deployments/{deployment_id}/statuses` | `deployment.status` | `deployment.status.create` |
| GET | `/repos/{owner}/{repo}/environments` | `environment.read` | `environment.list` |
| GET | `/repos/{owner}/{repo}/environments/{environment_name}` | `environment.read` | `environment.view` |
| PUT | `/repos/{owner}/{repo}/environments/{environment_name}` | `environment.put` | `environment.put` |
| DELETE | `/repos/{owner}/{repo}/environments/{environment_name}` | `environment.delete` | `environment.delete` |
| GET | `/user` | `user.read` | `user.view` |
| PATCH | `/user` | `user.update` | `user.update` |
| GET | `/notifications` | `notification.read` | `notification.list` |
| PUT | `/notifications` | `notification.mark_read` | `notification.mark_read` |
| PUT | `/repos/{owner}/{repo}/notifications` | `notification.mark_read` | `notification.mark_read` |
| PATCH | `/notifications/threads/{thread_id}` | `notification.mark_read` | `notification.mark_read` |
| DELETE | `/notifications/threads/{thread_id}` | `notification.done` | `notification.done` |

### L2 limitations

The api.github.com snapshot is the default and only description. GHES-only routes
absent from it remain unknown writes or heuristic GET/HEAD reads; no GHES variants
are generated. Shared public routes can match on GHES without proving availability
for its version. Full OpenAPI body validation, auth/outcomes, redirects, configured
API remapping, and confirmed side effects are not inferred. Host values describe
canonical identity, not transport destination. Unavailable bodies can prevent a
refinement without hiding the underlying known write.
`{branch}` is retained as deferred intent even where upstream rejects it with
GH_REPO set. The combined parameter view loses the query value on a body collision.
JSON numbers use Go JSON decoding (`float64` internally); typed fields use `int`.

## L3: GraphQL AST

[Request reconstruction](../internal/normalize/graphql/request.go),
[AST analysis](../internal/normalize/graphql/analysis.go), and
[normalizer](../internal/normalize/graphql/graphql.go) use gqlparser/v2 parsing,
not regex extraction. The [semantic registry](../internal/normalize/graphql/registry.go)
has 39 curated mutations; the [schema snapshot](../internal/normalize/graphql/schema_mutations.go)
contains all 278 public mutations captured on 2026-10-15, including input types,
field type signatures, candidate target IDs, descriptions and deprecation.
The snapshot provides default write classification, not full schema validation.
Regeneration and override curation are described in [GraphQL notes](notes/graphql.md#mutation-schema-snapshot).

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

### Schema-backed default mutation fields

The remaining 239 snapshot entries use **family `graphql.mutation`**, action
**`graphql.<field>`**, effect **write**, exact confidence, and no `unknownReason`.
Here `<field>` is each listed schema name; aliases never change it. Resolved input
is retained in parameters. Detail includes the schema description's first line
and deprecation when present.

Candidate targets are scalar `ID`/`ID!` fields named `*Id` (plus bare `id`).
Generation prefers the unique longest object noun matching the mutation name;
otherwise a sole candidate is selected. `addReaction` therefore uses `subjectId`,
and `transferIssue` prefers `issueId` over `repositoryId`. When several candidates
remain ambiguous, `target.nodeId` stays empty and an unresolved entry lists them,
even if only one value is supplied. Missing selected IDs remain unresolved.
Every mutation keeps `target.repository` unresolved with source `api`.

**Policy migration:** previously these schema-known fields were family `unknown`,
action `graphql.mutation.<field>`, with `unknown-mutation`, so
`policy.unknownMutation: deny` blocked them. They are now classified writes and
that setting alone no longer blocks them. Match `operation: "graphql.*"` to apply
a policy rule to GraphQL defaults (also matching query/unknown actions); combine
with `effect: write` to restrict the rule to writes. Curated actions such as
`pr.merge.direct` retain their existing names and need their existing matches.

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

A field absent from the snapshot still uses family `unknown`, action
`graphql.mutation.<field>`, effect write and reason `unknown-mutation`, with detail
`not in schema snapshot (2026-10-15)`. For example, `deleteRepository` remains
absent and subject to unknown-mutation handling.

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
not that analyzing an explicitly provided file cannot call that reader. All three
layers can use it. Missing data remains unresolved/unknown.

The wrapper/inspection CLI checks alias-expanded API arguments for `--input -`
or a typed `-F key=@-`. It does not pre-read native interactive input such as
`auth login --with-token`, `secret set`, or `workflow run --json`; API parse errors
also skip this pre-read. Raw `-f query=@-` is a literal string, not a stdin request.

Wrapper capture additionally requires a potentially relevant enforce-mode
denial (rules, unknown-mutation handling, or protected repositories) or an
enabled pre-hook. Command prefix/normalization level can exclude candidates;
body-dependent action/effect/target matches remain conservative before capture.
Observe logging, audit-only policies, and post-hooks alone do not pre-read stdin:
normalization/logs retain `stdin not captured` uncertainty while gh starts
without waiting for producer EOF. `ghok normalize`/`explain` still read requested
non-TTY input independently of configured enforcement.

For native text flags, wrapper capture instead requires a potentially matching
enabled pre-hook. The existing parsed text provenance identifies stdin, including
bundled `-F` shorthands; known native operation/effect/target matches may exclude
irrelevant hooks before capture. Inspection commands do not pre-read native
stdin; their native stdin texts remain explicitly `not-captured`.

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
| Native | All 243 static baseline registry paths and 57 built-in alias spellings; registered flag/selector extraction; normalized prose on mapped commands; plain alias chains. | Context lookup, interactive/generated text, runtime effects, enum/complete Cobra validation; heuristic unknown flags on known leaves. | Unregistered/newer commands, external extension effects, shell aliases, alias failures, several runtime integrations. |
| REST | All 1,242 pinned public OpenAPI operations; 206 canonical operationId overrides; supported API argv/body/query construction, host/path encoding and body-driven refinements. | Unavailable bodies/context, combined query/body collisions, gh-config transport remapping, GHES version/validation/outcome. | GHES-only/newer routes, full request validation, redirects, unusual/malformed paths, arbitrary absolute URL semantics; writes on unregistered routes retain unknown family. |
| GraphQL | AST selection/variables/aliases/fragments/directives; 278 schema-backed writes including 39 curated mappings, generated target-ID selection. | Unavailable variables/node lookup, ambiguous IDs, schema/version drift, nonstandard transport acceptance, execution beyond syntactic selection. | Full schema/type/authorization validation, batching/subscriptions, multiple stdin-consumer fidelity; snapshot-absent fields remain unknown writes. |

Normalization is extensible by editing the existing static registries; it does
not load runtime normalizer plugins. Context resolution is a separate layer and
never turns normalization intent into a post-execution side-effect confirmation.
Implementation notes retain design/regeneration/evidence details:
[native](notes/native.md), [REST](notes/rest.md), [GraphQL](notes/graphql.md), and
[context resolution](notes/resolve.md).
