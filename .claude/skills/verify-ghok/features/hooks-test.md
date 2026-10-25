# hooks test

`ghok hooks test` lets a user check how configured hooks and policy would decide a gh request. It executes trusted pre-hooks (or post-hooks with a synthetic result) but never runs the gh command.

## Sub-features

- `ht-deny` reports Hooks, Policy and FINAL decisions for a denied request.
- `ht-expect` `--expect`, `--expect-hooks`, `--expect-policy` turn decisions into exit 0 or 1.
- `ht-print-input` `--print-input` prints the hook input JSON without running hooks.
- `ht-no-gh` none of the above starts gh.

## How to get to it (user POV)

- Run `ghok hooks test [flags] -- <gh args>`.

## Driving it with control-ghok

Preconditions:

- Baseline run from `$S up` and passing `$S doctor`.

- **Deny report.** Run `$S ghok --label ht-deny -- hooks test -- pr merge 12 --admin -R acme/widgets`. Exit 1, stdout has `Synthetic hook test: original gh command is not executed.`, a JSON hook line with `"decision":"deny"`, `Hooks: deny`, `Policy: allow`, `FINAL: deny (mode=enforce`. `gh-started: no`.
- **Matching expectation.** Run `$S ghok --label ht-expect -- hooks test --expect deny --expect-hooks deny --expect-policy allow -- pr merge 12 --admin -R acme/widgets`. Exit 0, `gh-started: no`.
- **Failing expectation.** Run `$S ghok --label ht-expect-miss -- hooks test --expect allow -- pr merge 12 --admin -R acme/widgets`. Exit 1, stderr `ghok: expected FINAL decision allow, actual deny`.
- **Abstaining hooks.** Run `$S ghok --label ht-abstain -- hooks test --expect allow --expect-hooks abstain -- pr view 1 -R acme/widgets`. Exit 0.
- **Input preview.** Run `$S ghok --label ht-input -- hooks test --print-input --hook deny-admin-merge -- pr merge 12 --admin -R acme/widgets`. Exit 0, stdout is one JSON document with `"hookId": "deny-admin-merge"`, `gh-started: no`.

## Gotchas

- `--expect` compares FINAL, not the hook aggregate. Policy allow with abstaining hooks is FINAL `allow`, so `--expect abstain` does not match it.
- `hooks test` exit codes are 0/1/2, never 77/78.
- `--allow-api` cannot be combined with `--print-input`.
