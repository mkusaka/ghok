# ghok verification map

This directory is the maintained source for verifying ghok's user-facing behavior. Read the index before driving ghok, then use the matching feature file as the recipe.

## Baseline preconditions

- `S=.claude/skills/verify-ghok/control-ghok` from the repository root.
- A fresh run from `$S up` (baseline config) or `$S up --no-config`, with `VERIFY_GHOK_RUN` set to the printed run root on every call.
- `VERIFY_GHOK_RUN=<run> $S doctor` exits 0 (the `warn gh-version` line is expected).
- Baseline config: `mode: enforce`; rule `no-repo-delete` denies `repo.delete`; pre-hook `deny-admin-merge` on `pr.merge.*` denies `--admin` (`onError: deny`).
- Work repo `<run>/work` has remote `origin https://github.com/acme/widgets.git`.
- Never drive the user's own `~/.local/bin/gh` or `~/.config/ghok`.

## Driving conventions

- Run wrapped gh with `$S gh --label <name> -- <gh args>` and management commands with `$S ghok --label <name> -- <ghok args>`.
- Pass `-R acme/widgets` when the target repository matters; without it the repository is resolved from the work repo's git remote.
- Change config by editing `<run>/home/.config/ghok/config.yaml`; restore the baseline afterwards or start a new run.
- Treat every command as literal.

## Proof and skip reporting

- Proof is the bundle under `<run>-evidence/NN-<label>/`: command, exit code, stdout, stderr, and when present `gh-record.jsonl` and `ghok-log.jsonl`.
- Denial proof needs exit code, the `ghok: denied by` line, and no `gh-record.jsonl`.
- Allow proof needs exit 0 and a `gh-record.jsonl` with the original argv.
- "Does not run gh" claims need `gh-started: no`.
- Record the feature ID and entry point with each bundle. Report an unreachable path with the attempted command and the unmet precondition. Do not report a skipped entry point as verified through another path.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible behavior, then exactly four H2 sections in this order: `Sub-features`, `How to get to it (user POV)`, `Driving it with control-ghok` (starting with `Preconditions:`), `Gotchas`.

## Features

- [Wrapped gh: policy and hooks](./wrapper-policy.md) covers passthrough, rule deny, hook deny, hook error, and allow through the shim.
- [hooks test](./hooks-test.md) covers synthetic hook runs and `--expect*` checks without running gh.
- [explain and normalize](./explain-normalize.md) covers dry analysis of a gh request.
- [Shim and doctor](./shim-doctor.md) covers `shim install`, `doctor` and `shim uninstall`.
- [Config and exec](./config-exec.md) covers `config init/path/validate`, `exec` and `shell-init`.

## Not yet mapped

Project `.ghok.yaml` and `.ghok.local.yaml` layering, `config trust`, request transforms, observe-mode would-deny, post-hooks, `docs`/`actions`, and PTY/signal transparency.
