---
name: verify-ghok
description: Verify ghok (the gh-wrapping CLI with policy, hooks and JSONL logging) end to end by building it from this checkout and driving it through a disposable, isolated gh shim backed by a fake gh. Use when you need proof that a change to ghok's wrapper pipeline, policy, hooks, management commands (doctor, hooks test, explain, normalize, config, shim, exec) behaves correctly as a user would see it, not just that unit tests pass.
---

# verify-ghok

ghok is a CLI. The user-facing surfaces are the `gh` shim (a symlink named `gh`
pointing at `ghok`, which wraps every gh call) and the `ghok` management
commands. There is no server: "launch" means build into a disposable run root,
and every drive is a fresh process.

Everything goes through `.claude/skills/verify-ghok/control-ghok` (run it from
the repository root, or by absolute path). It isolates `HOME`,
`XDG_CONFIG_HOME`, `GHOK_CONFIG_DIR` and `PATH`, so the user's own ghok config,
logs and shim are never read or written.

## Why a fake gh

The real gh is an independently installed binary that ghok discovers on PATH
(or via `runtime.realGh` / `GHOK_REAL_GH`); that is a production boundary.
`control-ghok` puts the repo's hermetic `test/fakegh` there instead. It makes no
network calls, prints nothing, exits 0, and appends one JSON line (argv, argv0,
env, cwd, stdin) to `<run>/gh-record.jsonl` per invocation. That record is the
side-effect proof that gh did or did not start. Real-gh/network behavior is out of
scope for this skill.

## Launch

```sh
S=.claude/skills/verify-ghok/control-ghok
export VERIFY_GHOK_RUN=$($S up | tail -1)   # or: $S up --no-config
```

`up` (needs `go` on the caller's PATH) creates `${TMPDIR:-/tmp}/verify-ghok.XXXXXX`, then:

- builds `bin/ghok` from `./cmd/ghok`, `realbin/gh` from `./test/fakegh`, and
  `home/.config/ghok/hooks/deny-admin-merge` from `./examples/hooks/deny-admin-merge`;
- runs `ghok shim install --bin-dir <run>/shimbin` (owned symlink `shimbin/gh -> bin/ghok`);
- writes the baseline user config `home/.config/ghok/config.yaml` (skip with `--no-config`):
  `mode: enforce`, rule `no-repo-delete` (deny `repo.delete`), pre-hook
  `deny-admin-merge` on `pr.merge.*` with `onError: deny`;
- `git init`s `<run>/work` with remote `origin https://github.com/acme/widgets.git`.

The last stdout line is the run root. Shell state does not persist between tool
calls, so pass `VERIFY_GHOK_RUN=<run root>` on every later call.
Ready = `$S doctor` exits 0.

## Doctor

```sh
VERIFY_GHOK_RUN=<run> $S doctor
```

Runs `ghok doctor` in the isolated env and additionally fails unless
`ok real-gh:` is `<run>/realbin/gh` and `ok shim-ownership:` is `<run>/shimbin/gh`.
That is the "this is our instance, not the user's shim" check. Expected and
harmless: `warn gh-version: Unrecognized gh version output` (fakegh prints no
version). Do not "fix" it.

## Drive

```sh
VERIFY_GHOK_RUN=<run> $S gh   [--label NAME] -- <gh args>     # through the shim, cwd <run>/work
VERIFY_GHOK_RUN=<run> $S ghok [--label NAME] -- <ghok args>   # management CLI, cwd <run>/work
VERIFY_GHOK_RUN=<run> $S env                                  # print exports for manual drives
```

`gh`/`ghok` run the command, save a proof bundle, and print exit code, whether
gh started (record count before/after), the bundle path, stdout and stderr.
Stdin is passed through when piped and replaced with `/dev/null` when it is a
terminal (fakegh reads stdin to EOF). To change the config, edit
`<run>/home/.config/ghok/config.yaml` directly (YAML is re-read on every call).
Recipes per feature live in [features/README.md](features/README.md).

## Evidence

Each drive writes `${VERIFY_GHOK_EVIDENCE:-<run>-evidence}/NN-<label>/` with
`command`, `exit-code`, `stdout`, `stderr`, plus `gh-record.jsonl` (only if gh
started) and `ghok-log.jsonl` (only new ghok JSONL log lines). The evidence dir is
a sibling of the run root, so `down` keeps it.

Proof standards:

- Drive the real user path: `gh ...` through the shim, or the documented
  `ghok` command. Do not call internal Go functions or tests as proof.
- A denial is proven by all three: exit code (77 content deny, 78 hook/config
  error), the `ghok: denied by <source>` first stderr line, and **no**
  `gh-record.jsonl` in the bundle (gh never started). An allow is proven by exit
  0 and a `gh-record.jsonl` whose `argv` is the original request.
- Commands that claim not to run gh (`hooks test`, `explain`, `normalize`,
  `docs`, `actions`) must show `gh-started: no`. Observe that, do not trust the name.
- Pair the action with its logged state: `ghok-log.jsonl` `decision`
  (`allow|audit|deny`), `mode`, and `invocation.via` (`shim`/`exec`).

## Cleanup

```sh
VERIFY_GHOK_RUN=<run> $S down
```

Runs `ghok shim uninstall --bin-dir <run>/shimbin` and removes only the run
root. Nothing long-lived is started, so there is nothing to kill. Evidence stays
at `<run>-evidence`; check it still exists after `down`. Run `down` after every
failed attempt too.

## Isolation

- This machine may have a real ghok shim at `~/.local/bin/gh` and a user config
  at `~/.config/ghok`. Never drive them; always go through `control-ghok`.
- `PATH` inside a drive is `<run>/shimbin:<run>/realbin:/usr/bin:/bin`, so real
  gh (Homebrew, `~/.local/bin`) is undiscoverable. `go`, `jq` etc. are not on
  it either: build things before the drive, inspect evidence outside it.
- Each run has its own root, so parallel runs are safe.
