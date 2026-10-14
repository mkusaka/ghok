# Native normalization (gh v2.102.0)

## Design and supported behavior

`ExpandAlias(argv []string, aliases map[string]string) ([]string, string, model.UnknownReason)` and
`Normalize(inv model.Invocation, argv []string) model.Result` are pure. No filesystem,
git, stdin, configuration, or network reads occur. Callers supply environment/aliases;
`ReadFile` is deliberately not invoked. Targets requiring local git or API lookup are
recorded in `Unresolved` with source `git`/`api` and resolution `deferred`; unavailable
interactive/file/ambiguous values produce `partial`. External execution is `unknown`
and never mutation-free.

The static registry contains 243 paths (groups, built-ins, hidden help topics and Cobra
completion entry points) and 57 built-in alias spellings. Each entry declares flags,
positional interpretation, family/action/effect and its target extractor. All visible
v2.102.0 commands have flag tables, including inherited flags. Command discovery keeps
options appearing before the subcommand for the leaf parser. Flags support interspersed
positionals, `--name=value`, `--name value`, `-Rvalue`, `-R=value`, `-R value`, bundled
short booleans, explicit boolean values, repeat occurrences, and `--` termination.
Repeatable parameter arrays preserve each occurrence, including its literal CSV text;
they do not flatten commas or interpret enum/numeric values beyond booleans.

Repository selection: explicit positional repository/PR/issue URL overrides `--repo`,
then `GH_REPO`, with `GH_HOST` as the fallback host for owner/name. Full repository URLs,
host/owner/name, enterprise PR URLs, `/pull/N` and `/issues/N`, and trailing paths/query/
fragments are recognized. Number and `#number` selectors are recognized; PR branch
selectors retain `HeadBranch` and defer number lookup. Missing repository defers owner,
repository and host to git rather than assuming github.com; `GH_HOST` can fill host.
Missing PR selector defers current branch and its PR. Org/user secret/variable and org
ruleset scopes do not inherit the cwd repository. Repo commands which accept a repository
positional but do not expose `-R` still defer missing repository context.

Deep extraction covers PR/issue/repo/release/secret/variable/workflow/run/label/ruleset,
auth, extension, alias and config commands in the table. Flag parameters use lower camel
case; booleans are booleans, repeatable values are arrays, other values retain their argv
strings. Selectors unavailable in `Target` live in `Parameters`: `tag`/`assets`/`asset`,
`name`, `newName`, `workflow`, `run`, `ruleset`, `destinationRepository`, or `arguments`.
Workflow `inputs` combine raw and typed fields with typed fields taking precedence;
`@file`/JSON stdin values stay unresolved. `body`, `bodyFile`, `envFile` record presence
only, not their content/path. `auth token`, token-reading login, token-showing status,
and secret/variable set carry `sensitive`; login records `tokenFromStdin`.
Source argv remains unredacted for the shared dispatcher/output redactor: do not emit
normalizer results directly to untrusted hooks/logs without the output-boundary redactor.

`pr.merge` always describes intent, not a confirmed API side effect:

| Condition | Action |
| --- | --- |
| `--auto` | `pr.merge.auto.enable` |
| `--disable-auto` | `pr.merge.auto.disable` |
| `--admin` | `pr.merge.direct` |
| Otherwise | `pr.merge.request` |

All have family `pr.merge`, effect `write`, and exact intent confidence absent parse
uncertainty. The detail explains server/runtime choice of direct merge, auto-merge or
merge queue. Parameters include `method` (`merge`/`squash`/`rebase`/empty), `admin`,
`deleteBranch`, subject, body/bodyFile presence, `matchHeadCommit`, `authorEmail`.
`pr.review` specializes actions to `.approve`, `.request-changes`, `.comment`; the
interactive event stays unresolved. `repo set-default --view`, `issue develop --list`,
and `secret set --no-store` specialize to read actions listed below.

Known commands with unknown flags retain their effect/family/action, use heuristic
confidence, and include the flag in detail. Unknown arity conservatively discards
positional extraction and records `target.selector` as ambiguous; attached unknown
long values leave positionals usable. Unknown command paths can be extensions/newer
gh, so remain unknown. `extension exec`, Copilot and codespace execution/IDE integration
remain unknown even when their command identity is exact. Downloads are GitHub reads,
though they can write local files; checkout/clone/config/auth/install operations are
conservatively writes. This is not a local-filesystem security boundary.

Help/version options are mutation-free only when parsed as options, never when consumed
as a value or after `--`. `-h` follows a registered shorthand first: on auth it is
hostname, on config host, on repo create/edit homepage. Otherwise it denotes help.
Leaf `--version` is treated as non-mutating intent (gh may reject it as root-only).
Unknown/externally executed commands do not become safe merely because their argv
contains `--help`: extensions can interpret it arbitrarily. `extension exec` preserves
all forwarded arguments without flag parsing, matching its `DisableFlagParsing` setting
in [extension/command.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/extension/command.go).
Repo clone/fork keep arguments following `--` in `gitArguments`, never as clone directory.


## Verified upstream/POSIX behavior

- [cli/cli pkg/cmd/root/root.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/root/root.go):
  root `--help`/`--version`, built-in registration, extensions before aliases, alias
  validation, custom help behavior.
- [cli/cli pkg/cmd/root/alias.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/root/alias.go),
  [alias_test.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/root/alias_test.go),
  [alias/shared/validations.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/alias/shared/validations.go):
  plain aliases substitute before lexing. While any dollar remains, unused args are
  discarded; later args append once no dollar remains. `$1` also replaces the prefix
  of `$10`. Lingering `$digit` errors. NewCmdAlias re-enters `root.Execute`, so chains
  recurse, not expand once. Configured alias registration depends on existing commands
  and map iteration: forward chains may not be registered. Invocation does not include
  that registration order; expansion follows the supplied map. Only `argv[0]` is expanded;
  nested aliases/root-flag-prefixed alias names are not expanded by this API.
- [google/shlex shlex.go](https://github.com/google/shlex/blob/e7afc7fbc510/shlex.go):
  reproduced lexer uses ASCII whitespace, quote/escape rules, comments only between
  words, and no shell operator or environment expansion. Shell aliases starting `!`
  never execute/expand. Built-ins cannot be shadowed. Full-argv cycles and >64 steps
  produce `UnknownAliasLoop`; malformed lexing/missing placeholders are parse unknowns.
- [Cobra command.go](https://github.com/spf13/cobra/blob/v1.10.2/command.go),
  `stripFlags`, `argsMinusFirstX`, `Find`: unknown flag values are stripped during
  command traversal; the final leaf parses retained options. Verified directly with
  `gh -R org/repo pr merge 123 --help`: accepted and printed merge help, no RunE/API.
- [cli/cli internal/ghrepo/repo.go](https://github.com/cli/cli/blob/v2.102.0/internal/ghrepo/repo.go)
  and [pr/shared/finder.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/pr/shared/finder.go):
  repository formats and PR number/current-branch resolution.
- [cli/cli pr/merge/merge.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/pr/merge/merge.go):
  merge intent/queue behavior, mutual exclusions, body file semantics. Invalid flag
  combinations remain conservative write intents rather than predicted API operations.
- [cli/cli workflow/run/run.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/workflow/run/run.go):
  repeatable field arrays, raw-before-typed precedence, file-backed and JSON stdin inputs.
- [cli/cli pr/create/create.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/pr/create/create.go),
  [root/help.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/root/help.go),
  [actions/actions.go](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/actions/actions.go):
  creation context, rendered flag sections, and hidden Actions help-only command.

No shell/process primitives are used by this package. Its implementation is identical
on macOS/Linux and both architectures; there are no OS-specific files or dependencies.
Tests are hermetic and do not run real gh or touch HOME/PATH/config/network.

## Partial/unsupported cases

No git/API resolution, interactive choices, body/file contents, stdin values, enum
validation, complete Cobra argument-count validation or confirmation of effects.
Malformed known commands retain intent rather than claiming a confirmed side effect.
Subcommand discovery follows Cobra's consumption of unknown options: if unknown arity
hides the command itself, the command is unknown, not guessed safe. Raw repeated CSV
values are preserved, not expanded. `--json` without fields is gh's field-discovery
error; normalization reports a missing value instead of reproducing its field listing.
`secret get` is not a v2.102.0 built-in; it remains UnknownCommand. Unknown commands,
extensions and shell aliases are never safe. `api` is included only for registry
completeness; the dispatcher routes it to L2/L3 after plain alias expansion.

## Regenerating registry.go

Use exactly gh v2.102.0. In a temporary HOME/GH_CONFIG_DIR/XDG_CONFIG_HOME (empty config,
no installed extensions), run `gh help reference` and `gh help`. Parse each `##`/`###`/
`#### gh` heading up to its first usage placeholder into a command path; parse its
Aliases section into commandAliases. For each path, capture `gh <path> --help` and use
only FLAGS/INHERITED FLAGS sections, not examples or prose. The pflag column between
flag and two-space description separator supplies the value type; backtick placeholders
starting `--` in boolean descriptions (setup-git --force, cache succeed-on-no-caches)
are not types. Capture repeatability from source StringSlice/StringArray registrations
when a help placeholder conceals it. Add hidden root commands/help topics from root.go
and gh help, `co` as `pr checkout`, and Cobra completion entry points. Preserve the
reviewed semantic effect/positional annotations and attach extractTarget to each entry.
Probe flag arity with `gh <path> --help --<flag>` (no RunE); exported JSON field discovery
is a special diagnostic, and project item-edit --value takes a value despite no type
rendered in help. Run gofmt and native tests/fuzz. No generator is required at runtime.

## Full registry classification

Families/actions are dotted canonical command paths, retaining hyphens within verbs;
groups and version/help topics use `help`. Built-in aliases resolve to canonical paths.
The table gives base intent; specific flag-driven overrides follow it.

| Command | Family | Action | Effect |
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
| `pr merge` | `pr.merge` | `pr.merge` | write |
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

| Override | Family | Action | Effect |
| --- | --- | --- | --- |
| pr merge (default) | pr.merge | pr.merge.request | write |
| pr merge --auto | pr.merge | pr.merge.auto.enable | write |
| pr merge --disable-auto | pr.merge | pr.merge.auto.disable | write |
| pr merge --admin | pr.merge | pr.merge.direct | write |
| pr review --approve | pr.review | pr.review.approve | write |
| pr review --request-changes | pr.review | pr.review.request-changes | write |
| pr review --comment | pr.review | pr.review.comment | write |
| repo set-default --view | repo.set-default | repo.set-default.view | read |
| issue develop --list | issue.develop | issue.develop.list | read |
| secret set --no-store | secret.set | secret.set.encrypt | read |
| help / parsed --help / parsed --version | help | help | read |
