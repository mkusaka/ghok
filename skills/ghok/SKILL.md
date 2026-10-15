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

ghok is **not a security boundary**: the real gh by absolute path, `curl`, or any other
API client bypasses it. Server-side branch protection/rulesets are the real control.
Say so whenever a user wants ghok to "prevent" something.

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
```

Generated files are commented and start with a `yaml-language-server` `$schema` line.
Never overwrite an existing config; edit it instead.

- **Project `.ghok.yaml`** (repository root, commit it to share) automatically
  tightens policy and adds rules/protected repos; it cannot set `runtime` or
  `logging`. `trust.include` is project-only; user `trust.projects` is unsupported.
  Inspect hooks, then run `ghok config trust [PATH]` to activate them (`PATH`
  defaults to the current repository-root `.ghok.yaml`).
  The v1 manifest sorts unique repository-relative paths and encodes each path,
  executable bit and SHA256 content hash/missing marker separated by NULs. It
  covers exact YAML bytes, root-contained hook executables and `trust.include`
  files. Add imports/dependencies/data explicitly, for example
  `.github/scripts/check-pr-description.ts` and `.gitleaks.toml`.
  Project commands require `./relative` or absolute paths; user hooks may use
  bare PATH names. Relative files and symlink targets must stay under the repo
  root and be regular files. Limits: 256 files and global total bytes bounded
  by effective `runtime.fileMaxBytes`. Absolute external executables,
  interpreters, PATH tools and unlisted imports/dependencies/data are not hashed.
  Any covered byte or executable-bit change, including creation of a missing
  file, revokes trust for that content; notes name changed files. Policy-only
  configs skip manifest computation.
  Trust retains a digest set across versions/worktrees/clones. Locations are
  diagnostic, not digest inputs: identical code is trusted anywhere, not as a
  repository identity or a concurrent-replacement security boundary.
  Trust output lists every hashed file, hook IDs, resolved commands and coverage
  limits. `ghok config trust --list` shows digests, first paths, trusted timestamps,
  hook IDs, per-file executable bits and hashes/missing markers, plus store notes.
  Re-trusting records HEAD, resolved Git common directory and normalized origin;
  provenance never changes the digest. `ghok config trust --dry-run [PATH]`
  validates and prints the proposed trust without writing trust/blob/cache state.
  Trust, and validate/doctor when hooks are ignored, list changed/added/removed
  files and executable-bit mode changes against the newest record in the first
  matching tier: common directory, first path, normalized origin, then two
  distinct shared content SHA256s (not paths). One shared `.gitleaks.toml` is
  insufficient. Review the previous timestamp/path and, only in trust/dry-run,
  the unified diff against exact stored trusted bytes, including dirty files.
  The displayed diff masks credentials by default; when an agent runs
  `ghok config trust`, never add `--show-secrets` (the output stays in the
  transcript); `--no-diff` omits contents entirely. Stored copies are unredacted and may
  contain secrets: `<config-dir>/trust/blobs/<sha256>`, 0700 dirs/0600 files,
  1 MiB per file/64 MiB total. Missing/corrupt/over-limit copies omit diffs with
  notes. Binary files show short hashes; over-20,000-line sides omit the diff.
  No Git executable, object lookup or fetch is used; HEAD is list-only provenance.
  `ghok config untrust [PATH]` idempotently removes only the current digest,
  leaving policy and other versions; `ghok config untrust --all` clears all digests
  and purges stored copies. Trust/untrust garbage-collect unreferenced copies.
  Trust/current-only untrust reject missing/non-repository-root configs.
  Malformed store entries are skipped with named notes; other valid digests stay
  trusted. Invalid top-level stores grant no trust; declarative policy stays.
  Validate/doctor/explain report notes. The wrapper warns about enabled ignored
  hooks once per canonical project path/manifest/ten minutes, at most three
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

Nothing blocks unless `policy.mode: enforce`. When changing a user's policy to enforce,
confirm first and show `ghok explain` output for the commands they care about.

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
  - id: deny-admin-merge
    phase: pre
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
