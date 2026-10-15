---
name: ghok
description: Install, configure and operate ghok, a transparent wrapper for the GitHub CLI (gh) that runs pre/post hooks and enforces policies on gh commands, gh api REST calls and GraphQL mutations. Use when the user wants to block or audit gh operations (e.g. forbid `gh pr merge --admin`, require checks before merging, log merges), write or debug ghok hooks/rules, create or validate ghok config (`config.yaml`, project `.ghok.yaml`), find out how ghok classifies a gh command, or troubleshoot why gh is (not) going through ghok.
---

# ghok

ghok is one binary with two roles, chosen by the executable name:

- `ghok ...` — management CLI.
- `gh ...` via a shim (`~/.local/bin/gh -> ghok`) — transparent wrapper: classify the
  invocation, evaluate policy and pre-hooks, then run the real gh with the original
  argv/stdin/stdout/stderr/signals/exit status, then post-hooks.

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
ghok config schema [--project]       # JSON Schema (also referenced by the generated files)
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
  `ghok config untrust [PATH]` idempotently removes only the current digest,
  leaving policy and other versions; `ghok config untrust --all` clears all digests.
  Trust/current-only untrust reject missing/non-repository-root configs.
  Invalid or old-format stores grant no hook trust and emit a generic note;
  declarative policy remains active. Validate/doctor/explain report notes;
  hooks list/explain show ignored hooks. Ordinary wrapper stderr stays quiet.
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

- `ghok doctor` first. `command -v gh` must be the shim for the wrapper to apply.
- Deny message: `ghok: denied by policy: ...` or the hook's reason/message; exit 1 and
  the real gh was not started. Reproduce with `ghok explain -- <same args>`.
- Invalid project config: ignore only the project overlay; user hooks/policy remain
  active. Unknown project keys are ignored with notes. Discovery never uses HOME
  itself as a project root.
- Invalid user config: raw `policy.mode: enforce` prints an error every time,
  exits 1, and never starts gh; observe/undetermined mode runs gh **without**
  hooks/policy with a warning throttled to once per ten minutes.
  `GHOK_FAIL_CLOSED=1` rejects any invalid config, including project files.
  Validate/doctor explain the behavior; project failures also appear in explain.
- Logs (JSONL, credential-redacted): `<config dir>/logs/ghok.jsonl`.
  `GHOK_DEBUG=1` prints internal logging errors to stderr.
- Bypass for one command: run the real gh by absolute path. Hooks themselves run with
  `GHOK_NESTED=1`, so `gh` calls inside a hook skip hooks.
