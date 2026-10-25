# Read-only command delegates
<!-- desc: Route configured gh reads to another program, with safe fallback to real gh. -->

Delegates let agents keep using plain `gh` while a trusted program serves selected
reads, for example through an ETag-caching relay. They are **off unless configured**.
They replace only final execution: classification, transforms, resolution, policy
and pre-hooks still run first. A denial never starts a delegate. Post-hooks run
once with the final delegate or real-gh outcome, including exit 8 from checks.

## Configuration

Add an ordered top-level `delegates` list in user `config.yaml`, project
`.ghok.yaml`, or personal `.ghok.local.yaml`. User entries precede project entries,
then local entries. The first definite match wins; fallback goes directly to real
gh, never to another delegate. Matching uses the same `match`/`unless` rules as
hooks and transforms. Unresolved repository/host matches do not select a delegate.

```yaml
version: 1
delegates:
  - id: ghro-native
    match:
      level: L1
      effect: read
      operation: [pr.view, pr.checks, pr.list, pr.diff, run.view, run.list]
      repositories: [medley-inc/mall-cloud]
    unless:
      - operation: pr.checks
        parameters: {watch: "true"}
    command: ["bun", "scripts/ghro.ts", "gh"]
    timeout: 10s
    onError: fallback
  - id: ghro-api
    match:
      command: api
      level: L2
      effect: read
      method: GET
      repositories: [medley-inc/mall-cloud]
    command: ["bun", "scripts/ghro.ts", "gh"]
    timeout: 10s
    onError: fallback
```

This user-config example assumes the invocation cwd is the checkout root. Only the
**executable** path resolves against the config directory; other command arguments
and cwd are not rewritten. Use an absolute script path or a cwd-aware executable
when callers also run from subdirectories. Bun is an optional dependency of this
example, not a ghok runtime dependency. See [example config](../examples/delegates.yaml).

| Key | Default / constraint |
| --- | --- |
| `id` | Required; unique across delegates, hooks and transforms. |
| `match` | Required; explicit read-only constraints below. All criteria are AND; list entries are OR. |
| `unless` | Optional list of full matches. Only definite matches exclude operations. |
| `command` | Required, nonempty argv; no implicit shell. Original gh argv is appended. |
| `env` | Optional string overrides of the execution environment; ghok metadata wins. |
| `timeout` | `10s`; positive Go duration, at most `10m`. Bounds the delegate, not fallback gh. |
| `onError` | `fallback` (default) or `deny`; controls start failure and timeout before output. |
| `files` | Project only; extra repository-relative dependencies included in trust. |

`ghok config validate` rejects invalid entries. Existing configuration failure
behavior is unchanged: invalid project overlays are discarded without disabling
user policy; invalid user config fails closed in raw enforce mode or with
`GHOK_FAIL_CLOSED=1`, otherwise it runs real gh with the usual warning.

### Read-only constraints

Every delegate must explicitly set `match.effect: read` and select only
`match.level: L1` (native commands) or `L2` (REST). Selecting L2 additionally
requires exactly `match.method: GET`. A separate native and API entry is usually
clearest. The `method` matcher is also available to hooks, transforms and rules;
it compares the normalized REST HTTP method on the same operation and never
matches native commands or GraphQL.

These constraints are configuration errors, not a runtime promise to retry a
write. `unless` blacklists cannot replace them. Non-GET API commands, all GraphQL
(including queries), and native write/unknown actions are outside delegation.
At execution ghok also requires a mutation-free **whole invocation**, no analysis
errors/unknown considered operations, and exact native classification. Unknown
native flags, shell aliases and extensions remain on real gh. Recognized and
unregistered REST GET routes can be delegated; ambiguous headers such as
`X-HTTP-Method-Override` cannot.

**Why no writes?** A failed or timed-out program may already have submitted a
request. Retrying a mutation could duplicate comments, merges or other effects.
Normalization classifies intent; it cannot prove a server did not perform a write.
This restriction is not a sandbox: trusted delegate code can still call any API.

### Project trust

Project delegates use the same per-command digest/path trust as project hooks and
transforms, including `files`, shared `trust.include`, changed-file review and
`ghok config trust --hook ID`. User entries run without project trust; untracked
personal local config follows the existing local trust rules. Untrusted/changed
delegates are ignored, **never inactive gates**, even with `onError: deny`.

Project executables must start with `./` or be absolute, as for hooks/transforms.
For the Bun example in a project config, use the absolute Bun executable path
and add `files: [scripts/ghro.ts]` to **each** delegate (or shared `trust.include`).
Interpreters, external executable contents and unlisted imports are not hashed.
A bare `bun` command is allowed only in user config. Inspect the implementation
and dependencies before trusting it: it receives credentials, raw argv and stdin.

## Execution contract

For a matching `gh pr view 123 -R org/repo`, the example runs:

```text
bun scripts/ghro.ts gh pr view 123 -R org/repo
```

Arguments after the configured command are unchanged, including aliases, spaces
and quoting already represented as argv. If a trusted transform changes the
request, both delegate and fallback receive the same successfully transformed
argv/environment that real gh would otherwise receive. No-op/env-only transforms
retain original argv. Delegate-specific env overrides apply only to the delegate,
never to fallback gh or post-hooks.

The delegate inherits cwd, stdin, and the final execution environment, plus its
configured env and these metadata overrides:

| Variable | Value |
| --- | --- |
| `GHOK_NO_DELEGATE` | `1`, automatically set to prevent recursive delegation. |
| `GHOK_DELEGATE_ID` | Selected ID. |
| `GHOK_ACTION` | Matched normalized action, e.g. `pr.view`. |
| `GHOK_REPO` | Resolved matched `owner/repo`, or empty if unavailable. |
| `GHOK_REAL_GH` | Discovered real-gh absolute path. |

The existing incremented `GHOK_DEPTH` remains. No new nested-policy-bypass token
is issued to delegates. A delegate calling plain `gh` will still pass through
policy/hooks; only delegation is disabled. Hook/resolver calls with the existing
verified nested token retain their existing real-gh passthrough behavior.

Stdout/stderr are streamed byte-for-byte to the caller. Delegation always uses
the existing process supervisor in the shell's process group, with the same
signal/job-control limits as supervised real gh. It uses streaming output pipes
to track whether retry is safe, so child stdout/stderr are **not TTYs**, even
without post-hook capture. No PTY or `GH_FORCE_TTY` emulation is added; cross-stream
ordering/color/pager behavior can differ. Post-hook output retention remains
opt-in and bounded; logs never include these output bodies.

Non-TTY stdin is delivered as a live stream without reading it all first. Bytes
read for the attempt (including pipe prefetch) are retained in an immediately
unlinked 0600 temporary file and replayed before unread input on fallback.
Regular-file offsets are restored on fallback. Replay storage grows with consumed
stdin, not the analysis byte limit; storage failure prevents unsafe fallback and
returns 78. TTY stdin is inherited and never pre-read: a delegate that may decline
or time out before output **must not consume terminal input**. This is part of
the delegate contract because terminal input cannot be rewound. Requests whose
analysis still needs an incomplete live stdin spool stay on real gh.

### Fallback and exit status

| Delegate outcome | ghok behavior |
| --- | --- |
| Exit **125**, no stdout | Not handled: warn once, then run real gh with the same request. This applies even with `onError: deny`. Diagnostic stderr is allowed. |
| Cannot start (e.g. missing/non-executable command) | One-line stderr warning; real gh with default `onError: fallback`, or exit 78 with `deny`. |
| Timeout before any stdout **or stderr** | Same `onError` handling. TERM, then KILL after the existing one-second grace if needed. |
| Timeout after output, or exit 125 after stdout | Never retry: warn, return 78. Existing partial output cannot be retracted. |
| Any other exit, including 1, 2, 4 and **8** | Return unchanged; no fallback. An executable that starts and exits 127 is not a start failure. |
| Signal / interrupted execution | Preserve the actual status/signal; never retry a canceled command. |

125 is not one of gh's documented 0/1/2/4/8 statuses and is reserved **only for
delegate executables**. Real gh exiting 125 is returned normally. Return it for
unsupported flags, formats or endpoints **before writing anything to stdout**.
Do not print a JSON error, banner or debug line to stdout and then decline. ghok
detects stdout contract violations and refuses fallback to avoid mixed responses.
A delegate's internal exception that exits 1 remains exit 1; map genuinely
unsupported requests to 125 explicitly.

Output pipes are drained after termination, with a 200 ms bound for descendants
retaining them. An output-copy/drain error prevents assuming an empty stream.
Signal forwarding and timeout cleanup target the direct child, as for real gh;
the delegate is responsible for its descendants. Policy/pre-hooks are not rerun
on fallback. Post-hooks run once after the final outcome, and cannot change it.
Fallback diagnostics are credential-redacted, terminal-sanitized one-line stderr
warnings and are retained in warning/diagnostic logs.

## Inspect and disable

```sh
ghok config validate
ghok doctor                         # checks delegate executable existence/permissions
ghok explain -- pr view 123 -R medley-inc/mall-cloud
ghok explain --json -- api repos/medley-inc/mall-cloud/pulls/123
ghok normalize -- pr checks 123 -R medley-inc/mall-cloud
GHOK_NO_DELEGATE=1 gh pr view 123     # normal policy/hooks, always real gh
```

Explain shows selection without running the delegate:

```text
  Delegate that WOULD run: ghro-native command=["bun","scripts/ghro.ts","gh"] timeout=10s onError=fallback (fallback: real gh; not-handled exit=125)
```

It reports `none` when no trusted read-only entry matches or the escape hatch is
set. Explain's JSON and normalize's existing top-level JSON result have an
optional `delegate` object with `id`, `command`, `timeout`, `onError`, `action`
and, when known, `repository`. No delegate env values are displayed. Normalize
remains pure inspection of the **original** request and does not run transforms;
explain previews selection after trusted transforms. An enforce denial prevents
the displayed delegate and post-hooks from executing. Neither diagnostic runs
the delegate or the requested gh command.
