# explain and normalize

`ghok explain` tells a user what policy would decide for a gh request and which hooks would run; `ghok normalize` prints the semantic operation JSON. Neither runs gh or hooks.

## Sub-features

- `ex-deny` explain shows a deny decision and its reason.
- `ex-hooks` explain lists hooks that would run.
- `norm-json` normalize prints operations with family, action, effect and target.
- `ex-no-resolve` `--no-resolve` leaves git-derived targets unresolved.

## How to get to it (user POV)

- Run `ghok explain [--no-resolve] -- <gh args>`.
- Run `ghok normalize [--no-resolve] -- <gh args>`.

## Driving it with control-ghok

Preconditions:

- Baseline run from `$S up` and passing `$S doctor`.

- **Explain deny.** Run `$S ghok --label ex-deny -- explain -- repo delete acme/widgets --yes`. Exit 0, stdout starts `Decision: deny (mode=enforce)` with `Deny reason: rule no-repo-delete`. `gh-started: no`.
- **Explain hooks.** Run `$S ghok --label ex-hooks -- explain -- pr merge 12 --admin -R acme/widgets`. Exit 0, stdout lists `deny-admin-merge` under `Hooks that WOULD run (pre)` and `Operation 0: pr.merge.direct`. `gh-started: no`.
- **Normalize.** Run `$S ghok --label norm -- normalize -- pr merge 12 --squash -R acme/widgets`. Exit 0, stdout JSON has `"action": "pr.merge.request"`, `"effect": "write"`, target `"repository": "acme/widgets"`, `"number": 12`.
- **Git-derived target.** Run `$S ghok --label norm-git -- normalize -- pr merge 12 --squash`. Target owner/repository come from the work repo remote. Repeat with `normalize --no-resolve --`; the target has no repository and `unresolved` names it.

## Gotchas

- Explain exits 0 even when it shows deny; do not read its exit code as the decision.
- Explain never runs hooks, so hook outcomes are not shown; use `hooks test` for that.
- `--admin` makes the action `pr.merge.direct`; without it `pr.merge.request`.
