# Config and exec

A user creates and validates ghok configuration, and can run the wrapper without a shim through `ghok exec` or a shell function from `ghok shell-init`.

## Sub-features

- `cfg-path` prints the user config location.
- `cfg-init` writes a starter user config and never overwrites one.
- `cfg-validate` accepts a valid config and rejects an invalid one.
- `exec-wrap` `ghok exec -- <args>` runs the same pipeline, logged with `invocation.via: exec`.
- `shell-init` prints a `gh` shell function calling `ghok exec`.

## How to get to it (user POV)

- Run `ghok config path`, `ghok config init --scope user`, `ghok config validate`.
- Run `ghok exec -- <gh args>` and `ghok shell-init zsh|bash|fish`.

## Driving it with control-ghok

Preconditions:

- A run from `$S up --no-config` for init; the baseline `$S up` for exec denial.

- **Path.** Run `$S ghok --label cfg-path -- config path`. Stdout is `<run>/home/.config/ghok/config.yaml`.
- **Init.** On the `--no-config` run, run `$S ghok --label cfg-init -- config init --scope user`. Exit 0, stdout `Created <run>/home/.config/ghok/config.yaml`, and the file exists. Run it again: exit 1, stderr `... config.yaml already exists; not overwriting`, file unchanged.
- **Validate.** Run `$S ghok --label cfg-validate -- config validate`. Exit 0, stdout `Configuration valid`. Then append `bogus: 1` to the config and rerun; exit 1, stderr `field bogus not found in type config.Config`. On the baseline (enforce) config stdout also says `Invalid config behavior: fail-closed`.
- **Exec allow.** Run `$S ghok --label exec -- exec -- pr view 2`. Exit 0, `gh-started: yes`, record argv `["pr","view","2"]` with `argv0` `gh`, log `invocation.via` `exec`.
- **Exec is not a bypass.** On the baseline run, run `$S ghok --label exec-deny -- exec -- repo delete acme/widgets --yes`. Exit 77, `ghok: denied by rule no-repo-delete`, `gh-started: no`.
- **Shell function.** Run `$S ghok --label shell-init -- shell-init zsh`. Stdout contains `function gh { ghok exec -- "$@"; }`.

## Gotchas

- The starter config is `mode: observe` by default, so it logs `decision: allow` or `audit`, never blocks.
- Once any user config exists, ghok logs every call; without one it is pure passthrough with no logs dir.
