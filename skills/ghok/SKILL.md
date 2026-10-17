---
name: ghok
description: Install, configure and operate ghok, a transparent wrapper for the GitHub CLI (gh) that runs pre/post hooks and enforces policies on gh commands, gh api REST calls and GraphQL mutations. Use when the user wants to block or audit gh operations (e.g. forbid `gh pr merge --admin`, require checks before merging, log merges), write or debug ghok hooks/rules, create or validate ghok config (`config.yaml`, project `.ghok.yaml`), find out how ghok classifies a gh command, or troubleshoot why gh is (not) going through ghok.
---

# ghok

ghok has an executable-name-selected shim and a management CLI:

- `ghok ...` — management CLI; `ghok exec -- <gh args>` explicitly runs the wrapper.
- `gh ...` via a shim (`~/.local/bin/gh -> ghok`) — transparent wrapper: classify the
  invocation, evaluate policy and pre-hooks, then run the real gh with the original
  argv/stdin/stdout/stderr/signals/exit status, then post-hooks.

`ghok exec` is useful for tests/CI/scripts without a shim on PATH. It runs the
same pipeline once, forces real-gh argv[0] to `gh`, and is **not a policy bypass**.
Hook input and logs record `invocation.via: "exec"` instead of `"shim"`.
`eval "$(ghok shell-init zsh|bash)"` (fish: `ghok shell-init fish | source`) defines a
`gh` function calling `ghok exec --`. In an interactive rc file, it applies only to
shells that read that file. For non-interactive zsh, put the guarded zsh `eval` in
`~/.zshenv`; non-interactive zsh reads it. For non-interactive bash, including `bash -c`,
Bash reads the file in `BASH_ENV` (not `.bashrc`), so export `BASH_ENV` from the parent
environment to a file that runs the guarded bash `eval`. That creates a bash function;
functions are not inherited from zsh. For interactive bash, put the bash `eval` in
`~/.bashrc`. These setups do not cover `/bin/sh` or direct `gh` launches that bypass
a configured shell; use the PATH shim for broader coverage across scripts and agent
shells.

ghok is **not a security boundary**: the real gh by absolute path, `curl`, or any other
API client bypasses it. Server-side branch protection/rulesets are the real control.
Say so whenever a user wants ghok to "prevent" something.

## Find offline docs and action names

Start with `ghok docs` (or `--json`) for the embedded section tree, then
`ghok docs <id>` to read a section and its subsections. Search with a quoted
English query: `ghok docs search "parallel hooks"` returns up to five JSON
results with version-matched links. `ghok docs --all` prints all shipped docs.
These commands work offline without gh or configuration.

Use `ghok actions [prefix]` (or `--json`), e.g. `ghok actions pr.merge`, to
discover curated action names, effects and static native/REST/GraphQL routes.
Request-dependent actions may have no static route. Include `comment.*` as
well as `issue.comment.*` / `pr.comment.*` when matching unresolved comments.

## Install and activate

```sh
brew install mkusaka/tap/ghok        # macOS / Linux (Homebrew)
# or: go install github.com/mkusaka/ghok/cmd/ghok@latest
ghok shim install                    # ~/.local/bin/gh -> ghok; never edits shell rc files
ghok doctor                          # PATH precedence, real gh, config, hook permissions
```

`~/.local/bin` must come before the real gh's directory on PATH (`ghok doctor` prints the
fix). Undo with `ghok shim uninstall` (removes only ghok's own shim).
Install the shim machine-wide rather than per repository: with neither a user
config nor applicable project `.ghok.yaml`, gh is exec'd directly without logs,
normalization, stdin capture, resolver, or warnings. A user config enables ghok
globally. direnv/mise is only an optional human helper; non-interactive agent
shells do not load direnv, and their allow lists are path-based, not hook trust.

**Ask before running `ghok shim install` / `uninstall` on the user's machine** — it
changes what `gh` means in every shell. Use `--bin-dir` for experiments.

`ghok version` uses injected release metadata, otherwise Go's embedded module
version and available VCS revision. Tagged `go install` builds report their tag;
unknown/`(devel)` versions remain `dev`, and doctor warns that features may differ
from the docs.

## Configure

```sh
ghok config init                     # <repo root>/.ghok.yaml (project scope, the default)
ghok config init --scope user        # global config.yaml (`ghok config path` shows where)
ghok config validate                 # all errors at once; exit 1 if invalid
ghok schema config                  # global config JSON Schema
ghok schema project-config          # project .ghok.yaml JSON Schema
ghok schema local-config            # personal .ghok.local.yaml JSON Schema
```

Generated files are commented and start with a `yaml-language-server` `$schema` line.
Never overwrite an existing config; edit it instead.

- **Project `.ghok.yaml`** (repository root, commit it to share) automatically
  tightens policy and adds rules/protected repos; it cannot set `runtime` or
  `logging`. `trust.include` is project-only; user `trust.projects` is unsupported.
- `.ghok.local.yaml` (repo root, personal, per checkout; `ghok config init --scope local`
  creates it and adds it to `.git/info/exclude`) loads after `.ghok.yaml`. Its hooks and
  transforms run without trust; it is ignored while git tracks it; policy only tightens.
  Inspect hooks, then run `ghok config trust [PATH]` to activate them (`PATH`
  defaults to the current repository-root `.ghok.yaml`).
  Trust is per hook: canonical defaulted definition + repository-local executable
  + `hooks[].files` + top-level `trust.include` (**every hook**). YAML formatting,
  comments, policy and unrelated hook edits/reordering do not affect trust.
  List position is not hashed; put explicit ordering guarantees in `dependsOn`.
  Use `files: [scripts/check.ts]` for hook-specific imports/dependencies/data;
  use `trust.include` for shared files such as `.gitleaks.toml`. Files are hashed
  once with every affected hook ID recorded. A change revokes only affected hooks.
  `files` is rejected for user hooks, which run without trust.
  Project commands require `./relative` or absolute paths; user hooks may use
  bare PATH names. `files` paths must be repo-relative, without `..` or globs;
  list regular files, not directories. Symlink targets must stay inside the repo.
  Limits: 256 unique files and total bytes bounded by effective `runtime.fileMaxBytes`.
  Missing files have markers; their creation also revokes affected trust.
  Absolute external executables, interpreters, PATH tools and unlisted files
  are not hashed. Policy-only configs skip manifest computation.
  A gate is a pre-hook with effective `onError: deny` (the default). In enforce
  mode a changed gate denies matching invocations with `config <id>` / exit 78.
  A new untrusted gate also denies when another project hook is trusted/enabled.
  Observe records would-deny; a never-trusted project only ignores hooks/warns.
  Trust follows content: a hook digest trusted anywhere is trusted in every
  worktree/clone. One record per repository (Git common dir) keeps the latest
  per-hook digests/definitions/file baselines plus earlier grants.
  `ghok config trust --hook ID` is repeatable; unselected hooks keep their old
  trust and review baselines. Omitting `--hook` trusts all project hooks.
  Trust output lists every hashed file, hook IDs, resolved commands and coverage
  limits. `ghok config trust --list` shows digests, first paths, trusted timestamps,
  hook IDs, per-file executable bits and hashes/missing markers, plus store notes.
  Re-trusting records HEAD, resolved Git common directory and normalized origin;
  provenance never changes the digest. `ghok config trust --dry-run [PATH]`
  validates and prints the proposed trust without writing trust/blob/cache state.
  Trust groups definition/file changes per hook, showing shared files once;
  validate/doctor/explain/hooks list identify per-hook reasons and inactive gates.
  Review uses the repository's own record first, otherwise provenance-based hints
  (common directory, first path, normalized origin, then two distinct shared
  hashes). Hints never authorize hooks. Review the previous timestamp/path and,
  only in trust/dry-run, the diff against exact stored bytes, including dirty files.
  The displayed diff masks credentials by default; when an agent runs
  `ghok config trust`, never add `--show-secrets` (the output stays in the
  transcript); `--no-diff` omits contents entirely. Stored copies are unredacted and may
  contain secrets: `<config-dir>/trust/blobs/<sha256>`, 0700 dirs/0600 files,
  1 MiB per file/64 MiB total. Missing/corrupt/over-limit copies omit diffs with
  notes. Binary files show short hashes; over-20,000-line sides omit the diff.
  No Git executable, object lookup or fetch is used; HEAD is list-only provenance.
  `ghok config untrust [PATH]` idempotently removes the whole project record,
  leaving policy; `--all` clears every record and purges stored copies.
  Trust/untrust garbage-collect unreferenced copies and reject missing/non-root configs.
  Malformed/old entries are skipped with named notes; other valid grants stay.
  Invalid top-level stores grant no trust; declarative policy stays.
  The wrapper warns about enabled ignored hooks once per canonical project path
  and ignored-hook IDs/digests/reasons per ten minutes, at most three
  changed filenames. User `logging.ignoredHooksWarning: false` opts out; denied,
  nested, invalid-config, disabled-hook-only and policy-only paths do not warn.
- **Policy** (`policy:`): `mode: observe` (default; log only, never blocks) or `enforce`.
  `unknownMutation: allow|audit|deny` for writes ghok cannot classify.
  `protectedRepositories` (owner/repo globs) + `protectedUnknown` (default `deny`)
  fail closed for unclassified or unresolved writes to those repos.
  `rules:` — `{id, match, decision: deny|audit, reason}`; no rule can override a deny.
- **Match** (rules and hooks): `operation` (action glob, e.g. `pr.merge.*`), `command`
  (gh command prefix, e.g. `"pr merge"`), `repositories`, `hosts`,
  `effect` (`read|write|unknown`), `level`, `token` (`app` = GitHub App installation token
  such as gain/Actions, `user`, `unknown`, `none` = gh's stored login). Criteria are ANDed; list items are ORed.
- `match.env` is a map of names (`[A-Za-z_][A-Za-z0-9_]*`) to case-sensitive
  `path.Match` value globs; every entry must match the invocation environment
  before hook-specific overrides. `"*"` means set/non-empty (even with `/`),
  `""` unset/empty. Applies to rules and pre/post hooks. Caller-controlled:
  a guardrail against mistakes, **not a security boundary**.
  Example deny rule: `{id: no-worker-merges, match: {env: {MALL_GH_WORKER: "1"}, operation: "pr.merge.*"}, decision: deny}`.

Ordinary policy/hooks block only in `policy.mode: enforce`. Transform
`onError: deny` failures are different: they stop execution with 78 in either mode.
When changing a user's policy to enforce, confirm first and show `ghok explain`
output for the commands they care about.

## Inspect before changing anything

```sh
ghok explain -- pr merge 123 --admin -R org/repo      # human-readable
ghok normalize -- api -X PUT repos/o/r/pulls/1/merge  # JSON (redacted)
ghok hooks list
ghok hooks test -- pr merge 123                       # runs hooks for real, never gh
ghok hooks test --print-input -- pr create -t Title -b Body -R org/repo
ghok hooks test --print-input --phase post --exit-code 8 --hook audit -- pr checks 123
ghok exec -- pr view 123 -R org/repo                   # real pipeline, no shim needed
```

`hooks test` reports **Hooks** (`aggregate.decision`: allow/deny/abstain),
**Policy** (`policy.decision`: allow/audit/deny, after mode handling), and
**FINAL** (`final.decision`, after combining policy, pre-hooks and configured mode).
`--expect allow|deny|abstain|audit` still compares FINAL: observe would-deny is `audit`;
policy allow plus abstaining hooks is FINAL `allow`, not `abstain`.
Use `--expect-hooks allow|deny|abstain` for hook aggregate (`abstain` when none matched
or all abstained) and `--expect-policy allow|audit|deny` for policy alone.
With any expectations, all must match for exit 0, even on denial/hook error;
mismatches exit 1 and name each failed expectation. Without expectations, FINAL deny
or hook execution error (nonzero/spawn failure, timeout, invalid response,
cancellation) exits 1, otherwise 0. Configuration/analysis failures exit 1;
usage errors exit 2.

`hooks test --print-input` never runs hooks or gh; `--phase post` includes the
synthetic result. `--hook ID` selects a matching hook and applies its `redactInput`.
The default preview masks credentials and reports `N values masked; use
--show-secrets for the bytes the hook receives` on stderr. Actual hook
`redactions` counters are preserved. Do not add `--show-secrets` in an agent
session: it exposes raw input in the transcript. That flag never overrides a
hook's `redactInput: true`. Preview rejects `--allow-api`.

`explain`/`normalize` never run gh or hooks. Use them to get the exact `action` names to
put in `match.operation`. Classification levels (ghok's own labels):
L1 native gh command, L2 `gh api` REST (method + typed route), L3 `gh api graphql`
(AST: every mutation field, aliases, fragments, directives, operationName).

Related actions share a family but are not the same side effect, e.g. family `pr.merge`:
`pr.merge.request` (`gh pr merge`: server may merge, queue or auto-merge),
`pr.merge.direct` (`--admin`, REST `PUT .../merge`, GraphQL `mergePullRequest`),
`pr.merge.auto.enable`, `pr.merge.queue.enqueue`. Unknown/unclassified operations are
never treated as safe.

## Writing hooks

A hook is any executable. It gets one JSON document on stdin (`schemaVersion: 1`,
`event: pre|post`, `invocation`, `classification`, `operations[]`, `policy`, and for
post hooks `result` with `exitCode`/`signal`). A pre-hook prints one JSON response:

```json
{"schemaVersion": 1, "decision": "deny", "reason": "...", "message": "shown to the user"}
```

Print the JSON contracts with `ghok schema hook-input` / `ghok schema hook-response`.
In a source checkout, generate TypeScript types with:

```sh
npx json-schema-to-typescript schema/hook-input.schema.json > hook-input.d.ts
npx json-schema-to-typescript schema/hook-response.schema.json > hook-response.d.ts
```

Typed arrays are omitted when empty, never `null`; absent arrays mean empty.
`schemaVersion` is `const: 1`: additive fields never bump it, renames/removals do.

- `decision`: `allow` | `deny` | `abstain` (empty stdout with exit 0 also = abstain).
- **Input is raw by default**, including original/expanded argv, normalized texts
  and parameters. Hook authors are responsible for secrets they receive.
- Set `redactInput: true` on hooks that forward input anywhere (notifiers, webhooks).
  Only that hook's copy is masked; `redactions` reports substitutions at the top
  level and on every Text, including zero. Counts are not unique-secret counts;
  zero does not prove content is secret-free.
- If complete inspection matters, deny when `truncated` is true or text is unavailable;
  deny on `redactions > 0` when masked input cannot be safely evaluated.
  Logs and displayed hook stderr/reason/message remain redacted.
- Non-zero exit, timeout or invalid JSON is an *error outcome*, handled by the hook's
  `onError` (`deny` = fail closed, pre default; `allow`; `ignore`, post default).
- Skip operations whose `execution` is `not-selected` or `skipped`.
- Hooks run in config order (user, then project), with GitHub Actions-style concurrency:
  `- parallel: [hooks]` blocks, `background: true` hooks, and `- wait: id|[ids]` /
  `- wait-all:` items (capped by `runtime.hookConcurrency`, default 4). A pre deny cancels
  all running hooks. `dependsOn` names earlier hooks only. There is no `priority`/`cancel`.
- Post hooks can be limited to gh's outcome: `match: {result: failure}` (non-zero exit,
  signal or start failure) or `match: {exitCode: [1]}`; test with
  `ghok hooks test --phase post --exit-code 1 -- <gh args>`.
- Exit 78 with `(error: ...)` means the hook itself failed (missing tool, timeout): read the
  `stderr:` lines under the entry and fix the environment; don't edit the PR/issue text.
  Exit 77 means a rule/hook rejected the content: fix the content.
- `GHOK_EVENT_SOURCE` (`shim|exec|test`) may only be used to skip side effects in tests.
- Post hooks never change gh's exit status. Hooks run as the user with the user's
  environment (credentials included) — treat them as trusted code; keep stdout for the
  JSON response and write diagnostics to stderr.

```yaml
hooks:
  pre:                       # or post:; the list sets the phase (no phase: key)
    - id: deny-admin-merge
      match: {operation: "pr.merge.*"}
      command: ["./hooks/deny-admin-merge.py"]   # relative to the config file
      timeout: 10s
      onError: deny
```

Worked examples (Go and Python): <https://github.com/mkusaka/ghok/tree/main/examples>.
Full protocol: <https://github.com/mkusaka/ghok/blob/main/docs/hook-protocol.md>.

## Troubleshooting

- `ghok doctor` first. `command -v gh` must be the shim for ordinary `gh` calls;
  `ghok exec -- <args>` uses the wrapper explicitly without a shim.
- **Exit 77 (`EX_NOPERM`)**: policy/rule or hook-returned content denial; gh was
  not started. Read the denial, inspect `ghok explain -- <same args>`, and fix
  the requested operation or seek the policy owner's decision. Do not bypass
  by switching to real gh, `curl`, nested markers or `ghok exec`.
- **Exit 78 (`EX_CONFIG`)**: hooks malfunctioned under `onError: deny`, or invalid
  config failed closed; gh was not started. Run `ghok config validate` / `doctor`,
  inspect the named hook's timeout, executable/dependencies or response, and
  repair the environment/config. Do not treat it as a content-policy rejection
  or weaken enforcement to make the command pass.
- Denial stderr starts `ghok: denied by <source>` with optional
  `(error: timeout|exit <code>|invalid response|spawn failed|canceled)` and
  `(+N more)`. Indented entries list all denials: rules, policy, hook content,
  then hook errors. A mixture exits 77. Messages take precedence over reasons,
  with multiline content preserved; logs retain both structured fields.
- Observe-mode would-deny and all post-hook results preserve gh's original
  status/signals. Internal errors remain 1, real gh missing 127. `hooks test`
  expectation mismatches and hook failures remain 1, not 77/78.
- Invalid project config: ignore only the project overlay; user hooks/policy remain
  active. Unknown project keys are ignored with notes. Discovery never uses HOME
  itself as a project root.
- Invalid user config: raw `policy.mode: enforce` prints an error every time,
  exits 78, and never starts gh; observe/undetermined mode runs gh **without**
  hooks/policy with a warning throttled to once per ten minutes.
  `GHOK_FAIL_CLOSED=1` rejects any invalid config, including project files.
  Validate/doctor explain the behavior; project failures also appear in explain.
- Logs (JSONL, credential-redacted): `<config dir>/logs/ghok.jsonl`.
  `GHOK_DEBUG=1` prints internal logging errors to stderr.
- Bypass for one command: run the real gh by absolute path. `GHOK_NESTED=1` alone
  no longer bypasses hooks. Hook/resolver helpers receive it with an invocation
  token verified under `<config dir>/state/nested/`; normal cleanup removes it.
- `runtime.resolveAPI: auto` may resolve repository/host criteria for rules or
  enabled pre/post hooks in either mode; logging alone never queries. `never`
  disables implicit API resolution without treating unresolved targets as safe.

## Request transforms

Use `transforms.pre` for trusted request rewriting, not hook decisions.
Transforms run serially (user then trusted project), rematching the current
request before each entry, before policy/pre-hooks. Allowed definition keys:
`id`, `match`, `command`, `env`, `timeout` (5s default, 10m maximum), `onError` (`deny` default,
or `allow`), `redactInput`, project-only `files`. IDs share the hook namespace.
`transforms.post` is reserved; no parallel/background/wait/dependsOn/optional/
captureOutput settings are supported.

Programs receive hook JSON with `event: "transform"`, `phase: "pre"` and no
`previous`; empty stdout means no change. Otherwise return
`{"schemaVersion":1,"set":{"/operations/0/texts/body/value":"new body"},"unset":[]}`.
Paths are strictly limited to `/operations/N/texts/{title,body,notes}/value`,
`/operations/N/parameters/KEY` with a writable mapping, and
`/operations/N/target/{repository,number}`. Unsupported/ambiguous sources fail
as `unsupported-edit`; malformed paths fail as `invalid-response`.

The response may also contain a flat `env` object: strings set variables, `null`
entries unset them; `env: null` is invalid. Names must match
`^[A-Za-z_][A-Za-z0-9_]*$`, values cannot contain NUL. Forbidden names:
`GHOK_*`, `GH_PATH`, `GH_CONFIG_DIR`, `XDG_CONFIG_HOME`, `HOME`, `PATH`,
`LD_PRELOAD`, `LD_LIBRARY_PATH`, `DYLD_*`, `GH_HOST`, `GH_REPO`.
Credential names `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`,
`GITHUB_ENTERPRISE_TOKEN` are allowed and recompute host-scoped token kind;
other valid names are unrestricted. Invalid edits are `invalid-response`.
Request/environment edits are atomic; later entries win. Selection and command
environment use state before each transform; no self-retriggering.
Final `tokenKind`, `match.token` and `match.env` reflect edited environment.
gh and POST processes receive the edited environment; PRE processes retain the original environment.
PRE input still describes post-edit tokenKind. Environment-only edits preserve
original execution argv. Sorted `invocation.envChanged` and logging `env`/`envBy`
report names/attribution only. Environment values are never shown in explain or
hooks test, even with `--show-secrets`; all set values enter the shared redactor
before diagnostics/hook input.

Native curated flags, REST JSON `--input`, GraphQL variables/`--input`
variables and existing REST field prose are supported. Do not promise GraphQL
literals/defaults, shared variables across selected fields, unavailable/generated
text or arbitrary non-text REST field insertion. Edited requests must pass a
semantic round trip. Changed requests use expanded argv; no-ops preserve the
original. File/stdin write-back uses private 0600 transport and never overwrites
original files.

Project transforms require the same explicit trust, digests, dependency review
and warning handling as hooks, but inactive transforms are **never gates**.
On `deny` failure gh never runs (78, even observe); on `allow` failure the entire
chain rolls back both request and environment, remaining transforms are skipped,
and the original request/environment is checked/executed. Explain, hooks test and print-input apply transforms; normalize
does not. Logs summarize/redact changes rather than record replacement prose.
See `examples/transforms/attribute-session` for appending
`\n\nfrom session_id: $CLAUDE_CODE_SESSION_ID` to supplied bodies, with no output
when the environment variable is absent.

For trusted credential swaps see `examples/transforms/gain-token`: it runs
`gain [--profile $GAIN_PROFILE] token`, sets `GH_TOKEN`, and unsets `GITHUB_TOKEN`.
Use `match.operation: "pr.merge.*"` and `timeout: 15s` when minting needs more
than the 5s default. Review this trusted code and dependencies before activation;
redaction does not sandbox credential access.
