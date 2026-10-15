# Architecture

`ghok` is an argv-preserving wrapper with optional supervision, not an API proxy
or a sandbox. Classification, resolution, policy, hook execution, and process
control are separate so uncertain syntax does not become an asserted safe
operation, and ordinary commands do not require network context lookups.

## Module boundaries

| Module | Responsibility | Boundary |
| --- | --- | --- |
| `cmd/ghok` | Dispatch by basename of argv[0]; linker version/commit | `gh` invokes wrapper; management `ghok exec` explicitly enters the same pipeline. |
| `internal/cli` | Wire config/discovery/analysis/resolution/policy/hooks/process/logging | Preserve original execution argv and bytes; enforce only in enforce mode. |
| `internal/model` | Result, Operation, Invocation, APIRequest, ResolvedContext | Schema-version-1 shared contracts, not execution logic. |
| `internal/protocol` | Hook Input/Response/HookResult and decisions/outcomes | JSON stdin/stdout protocol. |
| `internal/config` | Defaults, strict YAML validation, alias reading, trusted project merge | No automatic execution of repository configuration. |
| `internal/schema` | Tagged reflection generator for config/project-config/hook-input/hook-response JSON Schemas | Development generation and `ghok schema`; not runtime hook validation. |
| `internal/install` | Real-gh discovery, owned shim lifecycle, depth guard, doctor | Stable PATH-facing paths; never modify real gh or shell rc files. |
| `internal/normalize` | Alias expansion then native/REST/GraphQL dispatch | No ambient filesystem/network/stdin access; injected Invocation reads only. |
| `internal/normalize/apiargs` | gh API flags and typed/raw nested fields | Shared reconstruction rules based on cli/cli 2.102.0. |
| `internal/normalize/native` | Command/flag registry, selector and intent extraction | Unknown syntax remains unknown. |
| `internal/normalize/rest` | Method + route matching, payload/target analysis | Not substring endpoint matching. |
| `internal/normalize/graphql` | AST, selection, variables, directives, mutation registry | No full server-schema/type validation or execution confirmation. |
| `internal/resolve` | Local metadata and explicitly needed read-only queries | Does not execute git/ssh; API calls use real gh directly. |
| `internal/gitmeta` | Shared bounded Git directory/ref/config readers | Local metadata only; no git execution, Git object lookup or network. |
| `internal/textdiff` | Anchored unified diff adapted from Go's BSD-3-Clause implementation | Exact trusted/current bytes; caller bounds size/lines and handles binary files. |
| `internal/policy` | Pure matching/evaluation across every considered operation | No config loading, hooks, or API calls. |
| `internal/hooks` | Trusted commands, dependency/concurrency scheduling, cancellation | Caller supplies raw Input; runner redacts only redactInput hooks. Diagnostics have a redaction callback. |
| `internal/process` | Exec/supervision, Unix signals/TTY/job control, bounded stdin replay | Single child/wait/signal owner; no real-gh process-group separation. |
| `internal/redact` | Display copies at output boundaries | Never change execution argv, payload, or replay bytes. |
| `internal/logging` | Redacted cooperating-process JSONL append/rotation | No stdout/stderr; errors cannot block execution. |
| `test/fakegh`, `test/e2e`, `test/bench` | Hermetic executable fixtures, integration tests, measurements | Temporary homes/configs, no GitHub writes. |
| `examples/hooks` | Executable pre-policy and post-audit examples | User installs them explicitly; not built-in policy. |

The Go packages are `internal`, not a supported public library SDK. Registry
updates are source changes; there is no runtime normalizer-plugin loader.

## Wrapper data flow

1. Handle installation probe before config/discovery/hooks.
2. Probe config presence only. With no user config and no applicable project
   `.ghok.yaml`, discover real gh, validate depth, and exec directly: no config
   parsing, logger, normalizer, stdin spool, resolver, or hook-warning check.
   Otherwise load user plus nearest-project config. Project failures discard
   only the overlay and retain user enforcement, notes, and log diagnostics.
3. Invalid user config with raw `policy.mode: enforce`, or any invalid config
   with `GHOK_FAIL_CLOSED=1`: log the failure, print an unthrottled error, and
   exit 78 without discovering/starting gh.
4. Otherwise discover real gh, excluding self/known wrappers and invalid PATH
   candidates; validate recursion depth and supply incremented `GHOK_DEPTH`.
   Invalid observe/undetermined user config logs at the default path, emits a
   throttled warning, and execs gh without hooks/policy.
5. Observe mode with no hooks/rules and logging explicitly disabled execs
   immediately without analysis. Before allowed execution, valid non-nested
   invocations warn once per project path/manifest/ten minutes about ignored
   enabled project hooks unless disabled by user logging config. Denied calls
   lead with the denial contract instead. Logging defaults on with any config.
6. Otherwise open best-effort logging. `GHOK_NESTED=1` plus a verified
   invocation token records passthrough and execs without analysis/policy/hooks.
7. Load aliases only if needed. For an analyzable API stdin consumer, spool
   non-TTY stdin only if a potentially enforcing denial or enabled pre-hook
   can need it; preserve every consumed byte. Observe logging/post-hooks alone
   leave stdin untouched and record unavailable analysis.
8. Normalize alias-expanded arguments. Real execution still uses original argv.
   Dispatcher selects API normalization only when expanded argv starts `api`;
   root flags before `api` currently remain L1 (see normalization limitations).
9. Resolve only context relevant to potentially matching repository/host policy
   or hook constraints/protected-repository handling. Evaluate policy; run
   selected pre-hooks with raw input (per-hook redactInput is opt-in). Any effective deny blocks only in
   enforce mode; content denial exits 77, hook-error-only denial 78; observe records would-deny.
10. Select post-hooks. Exec if none and stdin replay needs no live pump;
    otherwise supervise, deliver post result, log, and reproduce child status.

Resolver API calls and pre-hooks share a cancelable SIGINT/SIGTERM/SIGHUP
watcher, excluding startup-ignored signals. Stop/join it before Exec/Run;
restart after Run for post-hooks. Pre-phase interruption re-raises the signal
after helper cleanup; post-phase interruption is logged and preserves the
completed child status. SIGKILL cannot clean up helper processes.

```text
Invocation (original argv/env/cwd + injected bytes/reads/aliases)
       |
       v
Normalize -> Result [Classification, Operations[], Source, Errors]
       |                  |
       v                  +-> resolver fills requested missing targets
Policy Subject <----------+
       |
       +-> pure policy evaluation -> raw pre Input -> hook aggregate
       |
       +-> enforce deny: no real-gh start
       |
       v
Process Exec OR Run -> raw post Input -> post diagnostics
       |                                      |
       +--------------> redacted JSONL <-------+
       v
Original exit/signal (post failures do not override)
```

Raw normalized data can contain credentials. Hooks receive raw input by default;
the runner creates redacted copies only for `redactInput: true` hooks. The CLI
redacts log/normalize/explain and terminal boundaries; Logger also redacts its
own copy. Hook-input and log copies count credential substitutions independently.

The shim calls the shared wrapper with its original argv[0]; management
`ghok exec -- <gh args>` calls it once with argv[0] forced to `gh`.
Hook inputs and log records include `invocation.via: shim|exec`; discovery,
recursion depth/nested-token checks and every enforcement/execution path are
shared. Explicit execution does not install or require a PATH shim.

## Fast path versus supervised path

| Path | Trigger | Observable behavior |
| --- | --- | --- |
| No-config exec | Neither user config nor applicable project config | Presence/discovery/depth checks only; no logger/parser/resolver/spool/warning state. |
| No-analysis exec | Observe, no hooks/rules, logging disabled | Discovery/config/depth checks remain; no parser/resolver/spool. |
| Checked exec | No selected post-hooks, no live stdin pump | Pre-checks finish, log written before exec, wrapper is replaced. No final child status in that log. |
| Supervised inherited stdio | Selected post-hooks or live replay | Same process group and inherited stdio unless capture/spool requires pipes. Post-hooks receive exit/signal metadata. |
| Supervised capture | A selected post-hook requests captureOutput | Binary tee with bounded retained prefixes; child stdout/stderr are non-TTY pipes. Other post-hooks do not receive captured bodies. |

Exec-path duration measures analysis/pre-hooks, not gh runtime. Post result is
command outcome, not proof of server-side merge completion. Capture bodies are
excluded from logs. Transparency limits are explicit in
[compatibility.md](compatibility.md), including foreground pid-directed INT/QUIT,
Go's inherited signal dispositions, and core-dump reporting.

## Resolver staging and uncertainty

- **Arguments and environment:** parser target selectors, GH_REPO/GH_HOST.
- **Local metadata:** lazy repository-local git files/refs/config and basic SSH
  aliases, with provenance in ResolvedContext.Sources. No git/ssh executable.
- **Read-only API:** only requested missing fields relevant to a rule or enabled
  pre/post hook's repository/host criteria (either mode), or protected repositories
  in enforce mode; never for logging alone. `runtime.resolveAPI` defaults to
  `auto`; `never` disables implicit lookups. Helpers use the real gh absolute path,
  an invocation-scoped nested token, bounded output, and the resolve timeout.
  Failure retains maybe-matching and produces diagnostics. Management needs
  explicit `--allow-api`.

The resolver can look up an omitted PR number from a branch with exactly one
open match, or node-ID repository/number through a fixed GraphQL query. It
cannot safely borrow the cwd repository for a GraphQL node ID. Ambiguous remotes
can retain a candidate string and an unresolved marker: nonempty is not proof.
Unknown identity remains unknown after target resolution. Proven skipped or
unselected fields do not trigger API queries.

Local behavior is intentionally a bounded subset of gh's full factory resolver:
no system/global git config, conditional includes, insteadOf, reftable,
interactive fork discovery, or full ssh -G processing. Detailed supported and
unsupported forms: [resolver notes](notes/resolve.md).

## Design choices and rationale

- Separate family/action: `pr.merge.request`, direct merge, auto enable, and
  queue changes have related intent but different API effects. Never upgrade
  user intent to a confirmed effect.
- Separate confidence/resolution/execution: exact AST identity can coexist
  with an unresolved node target or a maybe-executed directive.
- Decisions are not execution outcomes: hook timeout/error cannot be parsed as
  a policy decision; pre onError controls fail-open/closed handling.
- Deny dominates: evaluate all considered operations; parallel completion
  cannot let allow undo denial. Results/message choice follow selection order.
- Trusted commands, not shell strings: argv execution avoids implicit shell
  parsing. Hook process groups permit timeout cleanup; real gh stays in the
  shell's group to preserve foreground job control.
- Secure bounded spool, not rewritten requests: preserve bytes even on analysis
  overflow/timeout, never pre-read a TTY; unknown handling decides enforcement.
- Stat/symlink discovery, not a cached Cellar target: Homebrew upgrades can move
  real binaries while stable PATH links remain.
- Explicit project trust and monotonic merge: repository files cannot silently
  install hooks or weaken global settings.
- Output redaction copies, not mutation: inspect/log safely without corrupting
  binary gh output or altering API requests. Redaction is best effort, not a
  confidentiality boundary against trusted hooks.
- Plain release shell rather than GoReleaser: four fixed targets, tar archives,
  checksums and provenance need no additional release dependency.

Detailed decisions and upstream source citations remain in [notes/](notes/).
