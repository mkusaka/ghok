# Shim and doctor

A user activates ghok by installing an owned `gh` symlink to ghok, checks the setup with `ghok doctor`, and removes only that symlink with `ghok shim uninstall`.

## Sub-features

- `shim-install` creates an owned symlink and probes it.
- `doctor-checks` reports real gh, PATH precedence, shim ownership, config, hooks and log writability.
- `shim-uninstall` removes only the owned shim, idempotently.
- `shim-refuse` refuses to overwrite a foreign `gh` in the bin dir.

## How to get to it (user POV)

- Run `ghok shim install [--bin-dir DIR]`, `ghok doctor [--json]`, `ghok shim uninstall [--bin-dir DIR]`.

## Driving it with control-ghok

Preconditions:

- Baseline run from `$S up`. `up` already ran `shim install --bin-dir <run>/shimbin`; its output is in `<run>/shim-install.log`.

- **Install output.** Read `<run>/shim-install.log`. It names `Installed ghok shim: <run>/shimbin/gh`, `Real gh: <run>/realbin/gh`, and `Shim probe passed.`
- **Doctor.** Run `$S doctor`. Exit 0 with `ok real-gh`, `ok path-precedence`, `ok shim-ownership`, `ok config`, `ok hook:deny-admin-merge`, `ok log-writable`, all under `<run>`.
- **Uninstall.** Run `$S ghok --label uninstall -- shim uninstall --bin-dir <run>/shimbin`. Exit 0, stdout `Removed <run>/shimbin/gh`, and `<run>/shimbin/gh` no longer exists. Running it again exits 0 with `No ghok shim to remove; other files are unchanged.`
- **Reinstall.** Run `$S ghok --label reinstall -- shim install --bin-dir <run>/shimbin`. Exit 0 and `$S doctor` passes again.
- **Foreign collision.** After uninstall, create a foreign file with `printf '#!/bin/sh\n' > <run>/shimbin/gh && chmod +x <run>/shimbin/gh`, then run `$S ghok --label refuse -- shim install --bin-dir <run>/shimbin`. Exit 1, stderr `... exists and is not managed by ghok (file); remove it or choose --bin-dir`, and the foreign file is unchanged. Remove it and reinstall before other features.

## Gotchas

- Always pass the same `--bin-dir` to install and uninstall; without it they target the user's `~/.local/bin/gh` inside the isolated HOME, not the run's shim.
- `warn gh-version` is expected with fakegh.
- With the shim removed, `$S gh` finds fakegh directly, so drives "pass" without ghok. Check `$S doctor` before trusting a wrapper result.
