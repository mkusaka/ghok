# Wrapped gh: policy and hooks

When a user types `gh ...` through the ghok shim, ghok classifies the request, applies policy rules and pre-hooks, and either runs the real gh with the original arguments or refuses before gh starts.

## Sub-features

- `wrap-passthrough` with no config, gh runs directly and ghok logs nothing.
- `wrap-allow` a request no rule or hook denies runs gh with the original argv and is logged `allow`.
- `wrap-rule-deny` a matching `decision: deny` rule blocks gh with exit 77.
- `wrap-hook-deny` a pre-hook returning deny blocks gh with exit 77.
- `wrap-hook-error` a pre-hook that cannot run under `onError: deny` blocks gh with exit 78.
- `wrap-fail-closed` an invalid user config that declares `mode: enforce` blocks gh with exit 78.

## How to get to it (user POV)

- Run `gh <args>` in a shell whose PATH puts the ghok shim first.

## Driving it with control-ghok

Preconditions:

- Baseline run from `$S up` and passing `$S doctor`, except `wrap-passthrough` which needs `$S up --no-config`.

- **Passthrough.** On a `--no-config` run, run `$S gh --label passthrough -- pr view 1`. Exit 0, `gh-started: yes`, `gh-record.jsonl` argv `["pr","view","1"]`, no `ghok-log.jsonl`, and `<run>/home/.config/ghok/logs` does not exist.
- **Allow.** Run `$S gh --label allow -- pr merge 12 --squash`. Exit 0, `gh-started: yes`, record argv `["pr","merge","12","--squash"]` with `argv0` `gh`, log `decision` `allow`, `invocation.via` `shim`.
- **Rule deny.** Run `$S gh --label rule-deny -- repo delete acme/widgets --yes`. Exit 77, stderr starts `ghok: denied by rule no-repo-delete`, `gh-started: no`, log `decision` `deny`.
- **Hook deny.** Run `$S gh --label hook-deny -- pr merge 12 --admin`. Exit 77, stderr starts `ghok: denied by hook deny-admin-merge` followed by `Remove --admin and use the repository's normal merge checks.`, `gh-started: no`.
- **Hook error.** Run `chmod -x <run>/home/.config/ghok/hooks/deny-admin-merge`, then `$S gh --label hook-error -- pr merge 12 --squash`. Exit 78, stderr starts `ghok: denied by hook deny-admin-merge (error: spawn failed)`, `gh-started: no`. Restore with `chmod +x`.
- **Fail closed.** Run `cp <run>/home/.config/ghok/config.yaml <run>/config.baseline`, append `bogus: 1` to the config, then `$S gh --label fail-closed -- pr view 1`. Exit 78, stderr starts `ghok: denied by config` and contains `fail-closed: not running gh`, `gh-started: no`. Restore with `cp <run>/config.baseline <run>/home/.config/ghok/config.yaml`; the next drive exits 0 with `gh-started: yes`.

## Gotchas

- A hook missing because it was never built also gives `(error: spawn failed)` and exit 78. It looks like a real denial; check `$S doctor` shows `ok hook:deny-admin-merge` first.
- `unresolved` repository fields in the log are expected when no `-R` is passed and no rule needs the target. They are not a resolution bug.
- `doctor` invokes fakegh too, so after `$S doctor` the record file is not empty at the first drive. Compare counts per drive, as the helper does.
- Rules match actions, not families: `pr.merge.*` covers `pr.merge.request` and `pr.merge.direct`; `pr.merge` alone does not.
