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
  tightens policy and adds rules/protected repos; it cannot set `runtime`,
  `logging` or `trust`. Inspect hooks, then run `ghok config trust [PATH]` to
  activate them (`PATH` defaults to the current repository-root `.ghok.yaml`).
  Any byte edit, including a git pull, disables project hooks until re-trusted.
  `ghok config untrust [PATH]` deactivates hooks idempotently, leaving policy.
  Both commands reject missing/non-repository-root files. Legacy `trust.projects`
  is ignored and deprecated. Trust hashes YAML bytes, not hook program contents.
  Validate/doctor/explain report trust notes; hooks list/explain show ignored
  hooks explicitly. Ordinary wrapper stderr stays quiet.
- **Policy** (`policy:`): `mode: observe` (default; log only, never blocks) or `enforce`.
  `unknownMutation: allow|audit|deny` for writes ghok cannot classify.
  `protectedRepositories` (owner/repo globs) + `protectedUnknown` (default `deny`)
  fail closed for unclassified or unresolved writes to those repos.
  `rules:` — `{id, match, decision: deny|audit, reason}`; no rule can override a deny.
- **Match** (rules and hooks): `operation` (action glob, e.g. `pr.merge.*`), `command`
  (gh command prefix, e.g. `"pr merge"`), `repositories`, `hosts`,
  `effect` (`read|write|unknown`), `level`, `token` (`app` = GitHub App installation token
  such as gain/Actions, `user`, `unknown`, `none` = gh's stored login). Criteria are ANDed; list items are ORed.

Nothing blocks unless `policy.mode: enforce`. When changing a user's policy to enforce,
confirm first and show `ghok explain` output for the commands they care about.

## Inspect before changing anything

```sh
ghok explain -- pr merge 123 --admin -R org/repo      # human-readable
ghok normalize -- api -X PUT repos/o/r/pulls/1/merge  # JSON (redacted)
ghok hooks list
ghok hooks test -- pr merge 123                       # runs hooks for real, never gh
```

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
- Invalid config: the wrapper warns once and runs gh **without** hooks/policy.
- Logs (JSONL, credential-redacted): `<config dir>/logs/ghok.jsonl`.
  `GHOK_DEBUG=1` prints internal logging errors to stderr.
- Bypass for one command: run the real gh by absolute path. Hooks themselves run with
  `GHOK_NESTED=1`, so `gh` calls inside a hook skip hooks.
