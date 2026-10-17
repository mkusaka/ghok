# Changelog

All notable changes to ghok. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Add entries under **Unreleased** as you change things; `scripts/release.sh` turns that
section into the new version and the release workflow puts it at the top of the GitHub
release notes. Mark changes that require users to update config, hooks or scripts under
**Breaking**.

## [Unreleased]

### Added

- User config `trust.skip: [paths]` runs project hooks and transforms without trust for repositories at those paths (trailing `/` = that directory and below, otherwise exact; `~` expands; linked git worktrees also match through their main worktree). Project configs cannot set it.

### Changed

- `ghok docs skill` (and its subsections) start with a comment giving the skill's source URL at the release tag matching the binary, for installing it with an agent's skill tool.

## [v0.0.15] - 2026-10-17

### Added

- Offline, version-matched `ghok docs` section browsing, full Markdown output
  and BM25 search; `ghok actions [prefix]` lists curated actions with their
  effects and statically mapped native commands, REST routes and GraphQL fields.

## [v0.0.14] - 2026-10-17

### Added

- Transform response `env` edits for trusted credential swaps and other allowed
  variables, atomic request/environment rollback, post-edit token matching and
  names-only redacted diagnostics; includes a `gain-token` Go example.

## [v0.0.13] - 2026-10-17

### Added

- `.ghok.local.yaml`: a personal, uncommitted config at the repository root, loaded after `.ghok.yaml` (like `CLAUDE.local.md`). Its hooks and transforms run without trust; it is ignored while git tracks it; policy can only tighten. `ghok config init --scope local` creates it and adds it to `.git/info/exclude`. Its JSON Schema is `schema/local.schema.json` (`ghok schema local-config`).

## [v0.0.12] - 2026-10-16

### Fixed

- `ghok exec` hands every following argument to gh unparsed; `--` is optional and no arguments (bare `gh` through the shell function) runs gh, which prints its help, instead of failing with a usage error.

### Added

- `gh --version` / `gh version` through ghok print `ghok version X (wrapping /path/to/gh)` after gh's own version lines when stdout is a terminal; piped output stays unchanged for scripts and hooks.

### Changed

- Homebrew caveats mention the `ghok shell-init` alternative to the shim and `ghok config init --scope user` (gh passes through untouched without a config).

## [v0.0.11] - 2026-10-16

### Added

- Trusted serial `transforms.pre` rewrites supported native flags/texts/targets,
  REST input and GraphQL variables before policy and hooks, with strict paths,
  atomic semantic round-trip checks, private file/stdin transport, project trust
  and redacted diagnostics. Failures deny with 78 even in observe mode, or
  `onError: allow` rolls the whole chain back to the original request.
- Session attribution transform example and transform-aware explain/hooks-test
  previews; normalize remains read-only and does not execute transforms.

## [v0.0.10] - 2026-10-15

### Fixed

- Trust follows content again: a hook trusted in one worktree or clone is trusted in every checkout with identical content (0.0.8 made trust per checkout path). Worktrees of a repository share one record that keeps earlier grants, so older branches stay trusted, and a changed gate is blocked the same way in every worktree instead of only in the one that trusted it.

## [v0.0.9] - 2026-10-15

### Breaking

- Hooks are grouped by phase: `hooks: {pre: [...], post: [...]}` replaces the single list with a per-hook `phase:`. `parallel`/`background`/`wait`/`wait-all` and `dependsOn` work within one phase list.

## [v0.0.8] - 2026-10-15

### Breaking

- Hook `priority` is removed: hooks run in config order (user config, then project), and `dependsOn` may reference only earlier hooks (a later hook is a config error; cycles can no longer form).
- Project trust is now per hook, not YAML bytes or a global content-digest set. One checkout-local record holds canonical hook definitions/digests; old records require `ghok config trust` again. Unrelated YAML edits no longer disable trusted siblings. Changed pre-`onError: deny` gates, or untrusted gates alongside active trusted project hooks, deny matching enforce invocations as `config <id>` (exit 78); observe records would-deny. `config untrust` removes the whole project's record.

### Added

- GitHub Actions-style hook concurrency: `- parallel: [hooks]` blocks, `background: true` hooks, and `- wait: id|[ids]` / `- wait-all:` items in `hooks`. A pre deny still cancels every running hook; project hooks start after user background hooks. `runtime.hookConcurrency` now defaults to 4. `ghok hooks list` shows `background` and the hooks each one waits for; the config schemas describe the new items.
- Project `hooks[].files` tracks hook-specific dependency files; `trust.include` applies to every hook. Repeatable `config trust --hook ID` authorizes selected hooks while preserving siblings' trusted digests and exact-byte review baselines. Review is grouped by hook/shared files; list/explain/validate/doctor and throttled warnings identify per-hook reasons and inactive gates.

## [v0.0.7] - 2026-10-15

### Added

- `ghok shell-init zsh|bash|fish` prints an eval-able `gh` shell function that runs `ghok exec --`, for adopting ghok without a PATH shim; the output carries a comment that gh started by scripts or other programs is not covered.
- `ghok exec` records its last use in `<config-dir>/state/exec-entry`. When it was used within seven days and no owned shim exists, `doctor` reports the shim PATH/ownership checks as info and adds a `shell-entry` check naming the child-process gap; with a shim it notes the shim also covers child processes.

## [v0.0.6] - 2026-10-15

### Breaking

- Enforce denial stderr now has a stable `ghok: denied by <source>` header and ordered per-denial entries, preferring multiline messages over reasons. Content denials exit 77 (`EX_NOPERM`); hook-`onError`-only and fail-closed config denials exit 78 (`EX_CONFIG`), replacing exit 1. Content wins over errors; observe and `hooks test` mismatch statuses are unchanged.
- Hook-authored terminal diagnostics now escape controls (except newline/tab) and invalid UTF-8, and bound each denial/text entry to 40 lines, 4 KiB and 500 characters per line with visible truncation markers. Denials, `hooks test` text, explain and hook/config notes share the sanitizer; JSON, `--print-input` and logs retain their structured values and existing redaction.
- Replace `ghok config schema [--project]` with `ghok schema config|project-config|hook-input|hook-response`. Empty typed hook arrays are now omitted instead of `null`; absent means empty.
- Without a user config or applicable project `.ghok.yaml`, the shim now execs gh directly without logging, normalization, stdin capture, resolution, or hook warnings. Logging remains default-on once either config exists; machine-wide shim installation is the recommended adoption path.

### Added

- Deny output for a malfunctioning hook (onError, exit 78) includes the last two non-empty lines of its stderr (redacted, sanitised, 200 bytes each; withheld for `redactInput` hooks), so causes like `env: bun: No such file or directory` are visible without the log.
- Hooks receive `GHOK_EVENT_SOURCE` (`shim`, `exec`, `test`) for skipping side effects in tests; the input JSON stays identical (`invocation.via` is never `test`). `hooks test --print-input` keeps stdout pure JSON and prints its source note on stderr.

- `ghok hooks test --print-input [--phase pre|post] [--hook ID]` previews hook JSON without starting hooks or gh, with synthetic post results, credential masking notices, and `--show-secrets` that still honors hook `redactInput`.
- `ghok exec -- <gh args>` runs the shim's exact pipeline without a PATH shim, preserving discovery/recursion checks, policy, hooks, logs, argv, streams, statuses and signals. Hook input and JSONL records include `invocation.via: "exec"|"shim"`; the hook-input schema is regenerated.
- Generated hook input/response JSON Schemas, closed-value enums and documentation from Go tags, `schemaVersion: {const: 1}`, and TypeScript generation instructions. Regenerate all four contracts with `go generate ./internal/schema`.
- Throttled stderr reminders for enabled project hooks ignored by trust, including up to three changed filenames; user `logging.ignoredHooksWarning: false` opts out.
- Bounded private persistent manifest hash cache under `<config-dir>/state/`, invalidated by canonical path and file metadata; project YAML is always read fresh.
- Repository-aware "what changed since trust" hints in `config trust`, `config validate` and `doctor`, with recorded HEAD/common-dir/normalized-origin provenance, changed/added/removed files and executable-bit changes; `config trust --dry-run` previews without state writes.

### Changed

- Trust review now compares exact stored trusted bytes, including dirty files, instead of the recorded commit. Private deduplicated copies are bounded to 1 MiB/file and 64 MiB total, integrity-checked and garbage-collected on trust/untrust; `untrust --all` purges them. Trust/dry-run show anchored unified diffs with credentials masked (`--show-secrets` reveals, `--no-diff` opts out); binary/large/missing/corrupt baselines show explanations. Removed Git object/pack-index lookup.

### Fixed

- A malformed trust-store entry is skipped with a named note instead of discarding other valid trusted digests.
- Manifest hashes now remain cached across projects, with oldest-first eviction beyond 4096 entries. `doctor` explains no-config passthrough and skips unused log-path checks.

## [v0.0.5] - 2026-10-15

### Breaking

- **Comment actions renamed (L1).** `pr.comment` / `issue.comment` are now `pr.comment.create|edit|delete` and `issue.comment.create|edit|delete`, shared with REST and GraphQL. Writes whose subject is unknown (REST `POST .../issues/{n}/comments`, GraphQL `addComment` before node resolution) are `comment.create`. **Update `match.operation`**; to never miss a comment write, also match `comment.*`. `ghok config validate` warns about patterns that match no known action.
- **`GHOK_NESTED=1` alone no longer bypasses hooks.** Passthrough now also requires a per-invocation token that ghok gives only to its own hooks and resolver. Scripts that set `GHOK_NESTED=1` to skip ghok must call the real gh by absolute path instead.
- **Hook input is raw.** `argv`, `texts` and `parameters` reach hooks exactly as gh receives them, including credentials (hooks already inherit `GH_TOKEN`; redacting their input only broke secret scanning). **Hooks that forward their input anywhere (Slack, webhooks, files) must set `redactInput: true`.** Logs, `explain`/`normalize` output and terminal messages are still redacted.
- **An invalid user config in `enforce` mode now stops gh (fail-closed).** If your global `config.yaml` is invalid and declares `policy.mode: enforce`, gh is not run (exit 1, message every time). Previously gh ran without hooks. Observe-mode configs still fail open with a warning. `GHOK_FAIL_CLOSED=1` makes any invalid config fail closed (useful in CI). Run `ghok config validate` after upgrading.
- Project trust records from v0.0.3 or earlier must be re-created once with `ghok config trust`.

### Added

- `hooks test`: separate Hooks / Policy / FINAL results; `--expect` (final), `--expect-hooks`, `--expect-policy`; reads stdin like the wrapper.
- Per-hook `redactInput: true` with per-text `redactions` counts; log records count redactions.
- `policy.unknownInput` (unreadable request) separate from `unknownMutation`; per-rule `onUnresolved: deny|ignore`; `runtime.resolveAPI: auto|never`; `GHOK_FAIL_CLOSED=1`.
- GraphQL node IDs are resolved (2 s timeout, on-disk cache) only when a rule or hook can act on the answer; `comment.create` specialises to `pr.comment.create` / `issue.comment.create`.
- One canonical action catalogue shared by all layers; `config validate` warns about patterns that match no known action.

### Changed

- A broken project `.ghok.yaml` or unknown keys in it only drop the project part (with a note); the user config keeps working. `$HOME` itself is never a project root.
- stdin analysis waits at most 3 s (was 30 s).
- `changed since trust` is reported only against this checkout's own trust record; otherwise `untrusted`.

### Fixed

- `kill -STOP` of gh no longer strands the wrapper; a failed exec is logged correctly.

## [v0.0.4] - 2026-10-15

### Breaking

- Project trust is content-addressed: a set of digests over `.ghok.yaml`, repository-local hook executables and `trust.include` files, so it survives worktrees and clones. Existing trust must be re-created with `ghok config trust`.
- Project hooks must use `./relative` or absolute commands (bare names are rejected).

### Added

- `operations[].texts` (`title`, `body`, `notes` with `source`, `bytes`, `truncated`) for L1, REST and GraphQL; `--body-file -` stdin is captured and replayed when a pre hook could need it.
- `match.env`, `ghok hooks test --expect`, `ghok config trust --list`, `ghok config untrust [--all]`.
- `ghok version` falls back to Go build info for `go install` builds; `doctor` warns on development builds.

## [v0.0.3] - 2026-10-15

### Changed

- Logs mask free text (titles, bodies, comments, GraphQL documents) as `[content: N bytes]`; repository/action/target stay readable. Hook input unchanged.
- Routine config-source notes are no longer written to every log record; unanalysed stdin is logged as `allow` with `stdin not analyzed`.
- Concise "real gh not found" error; invalid-config warning at most once per 10 minutes.

## [v0.0.2] - 2026-10-15

### Breaking

- Project `.ghok.yaml` rules, protected repositories and policy tightening apply without trust; only project hooks need `ghok config trust`.

### Added

- `match.token` (`app|user|unknown|none`), `match.result` / `match.exitCode` for post hooks, `hooks test --exit-code`.
- L1/L2/L3 registries generated from gh's cobra tree, GitHub's OpenAPI description and GraphQL schema.
- GitHub Action, Linux packages (deb/rpm), immutable releases, an Agent Skill.

### Changed

- `explain`/`normalize`/`hooks test` resolve local repository context by default; conclusion-first `explain`; cobra-standard help.

## [v0.0.1] - 2026-10-15

### Added

- First release: transparent `gh` wrapper with pre/post hooks, policy (observe/enforce), L1/L2/L3 normalization, JSON hook protocol, management CLI (`shim`, `doctor`, `explain`, `normalize`, `hooks`, `config`), JSONL logging with redaction, Homebrew tap.

[Unreleased]: https://github.com/mkusaka/ghok/compare/v0.0.15...HEAD
[v0.0.15]: https://github.com/mkusaka/ghok/compare/v0.0.14...v0.0.15
[v0.0.14]: https://github.com/mkusaka/ghok/compare/v0.0.13...v0.0.14
[v0.0.13]: https://github.com/mkusaka/ghok/compare/v0.0.12...v0.0.13
[v0.0.12]: https://github.com/mkusaka/ghok/compare/v0.0.11...v0.0.12
[v0.0.11]: https://github.com/mkusaka/ghok/compare/v0.0.10...v0.0.11
[v0.0.10]: https://github.com/mkusaka/ghok/compare/v0.0.9...v0.0.10
[v0.0.9]: https://github.com/mkusaka/ghok/compare/v0.0.8...v0.0.9
[v0.0.8]: https://github.com/mkusaka/ghok/compare/v0.0.7...v0.0.8
[v0.0.7]: https://github.com/mkusaka/ghok/compare/v0.0.6...v0.0.7
[v0.0.6]: https://github.com/mkusaka/ghok/compare/v0.0.5...v0.0.6
[v0.0.5]: https://github.com/mkusaka/ghok/compare/v0.0.4...v0.0.5
[v0.0.4]: https://github.com/mkusaka/ghok/compare/v0.0.3...v0.0.4
[v0.0.3]: https://github.com/mkusaka/ghok/compare/v0.0.2...v0.0.3
[v0.0.2]: https://github.com/mkusaka/ghok/compare/v0.0.1...v0.0.2
[v0.0.1]: https://github.com/mkusaka/ghok/releases/tag/v0.0.1
