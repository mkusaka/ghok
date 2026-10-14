# CLI integration

## Entry and execution

`cmd/ghok` dispatches by `filepath.Base(os.Args[0])`: `gh` is wrapper mode, every other name is management mode. `main.version` and `main.commit` accept linker injection. `GHOK_PROBE=1` in wrapper mode emits exactly `ghok-probe\n` before configuration, discovery, or hooks.

The wrapper passes original argv, inherited environment plus `install.NextDepth`, cwd, and binary streams to the discovered real gh. `GHOK_REAL_GH` overrides `runtime.realGh`; discovery rechecks PATH-facing executable paths. It does not rewrite GH_PATH. Alias expansion affects analysis/matching only, never execution argv. The native registry supplies the cheap built-in check, so built-ins do not read gh alias configuration. Unreadable alias config leaves the command unknown.

Missing config uses package defaults: observe mode, no hooks, **logging enabled**. Therefore no-config invocations still normalize and write a log. Observe mode with no hooks/rules and explicitly disabled logging skips normalization, stdin capture, and resolution. Nested `GHOK_NESTED=1` invocations bypass analysis/policy/hooks, still increment recursion depth, and log `passthrough-nested` when logging is enabled. Invalid effective config cannot establish policy: one concise, redacted stderr warning, then real-gh passthrough without hooks/logs. This availability choice is not fail-closed enforcement.

Pre-hooks and policy both run before gh. Enforce denies if either denies; deny exit code is 1 and no gh child starts. Observe never blocks: policy/pre-hook denials become `would-deny` records. Runner denial short-circuiting still applies within a pre-hook batch, including observe mode; canceled hooks remain diagnostic results.

`policy.Evaluation.DenyReasons` contains only deny-causing rule, unknown-mutation,
and protected-repository reasons, including the operation action where applicable.
It remains populated for would-be denials in observe mode. `Reasons` stays the
full diagnostic list for hooks/logging/explain. Enforce stderr uses only
`DenyReasons` plus the first effective hook denial's reason/message; hook denial
does not replace a policy denial. Explain shows deny reasons separately and
prints `Matched rules: none` when no rules match.

Without matching post-hooks or live stdin replay, `process.Exec` replaces the wrapper. Logs are written **before** Exec, so duration covers analysis/pre-hooks and no child exit status is available. Matching post-hooks or a spool needing a live pump use `process.Run`; post-hooks receive the exact child exit code/signal and never change it. Output capture is requested only by selected post-hooks. Non-capturing hooks receive execution metadata without captured bodies, even when another hook enabled capture.

One helper-phase watcher covers SIGINT/SIGTERM/SIGHUP across resolver API calls and pre-hooks, excluding signals ignored before any Notify call. Cancellation kills/reaps helper groups; before gh starts, `process.Result.Exit` re-raises the received signal. The watcher stops and joins before Exec/Run takes signal ownership, then restarts immediately after Run returns for post-hooks. Post interruption cancels/reaps hooks and records `post-hooks interrupted by SIG...` in diagnostics, but `execution.Exit` still returns gh's completed exit code/signal. The default observe/no-hook path allocates no helper watcher. SIGKILL cannot be handled; hooks may outlive ghok. Child execution/job-control/capture limitations remain those in [process.md](process.md), including foreground wrapper-directed INT/QUIT ambiguity and Go's inherited-disposition limits.

## Analysis and resolution

Stdin capture is only attempted for alias-expanded `gh api --input -` / `-F key=@-` when an enforce-mode deny rule, unknown-mutation/protected-repository denial, or enabled pre-hook could need the body. Candidate command prefixes and L2/L3 levels are checked before capture; body-dependent operation/effect/repository/host criteria cannot exclude a candidate yet. Observe logging, audit-only enforce policies, disabled/unrelated pre-hooks, and post-hooks alone leave stdin untouched and record `stdin not captured`; gh starts before producer EOF. Normalize/explain keep their non-TTY capture behavior. The spool never reads a TTY and preserves consumed bytes, including oversized/timeout prefixes. Files remain bounded by `runtime.fileMaxBytes`. Analysis copies are never replay data.

Local git/host resolution runs only for unresolved repository/host matches or potentially writing operations whose protected-repository membership is unresolved. API resolution is permitted only in enforce mode for missing fields relevant to a potentially matching pre-hook/deny rule or protected-repository decision. Each operation is resolved separately so an unrelated operation does not trigger an API query. Resolver timeouts and ambiguity remain explicit diagnostics/unresolved fields. Observe mode never implicitly calls the API. Management API resolution requires explicit `--resolve --allow-api`.

Hook input and logger records cross `redact.New(env)` boundaries. gh still receives original secrets and bytes. Logging errors never change execution and appear only with `GHOK_DEBUG=1`. Hooks are trusted executable code; ghok is not a security boundary, and direct gh/API access bypasses it.

## Management and platform

Management commands use stdlib flag parsing. Success is 0, failure is 1, usage errors are 2. Explain exits 0 even for a displayed deny; it does not run hooks or gh. Hooks test runs selected hooks with synthetic execution metadata for post phase, never executes the requested gh command. Enable/disable manage only owned shims/state and never edit shell rc files. Normalization JSON and explanations redact secrets unless explain explicitly requests `--show-secrets`.

Plain doctor checks sorted `state/shims.json` records, including custom enable locations, with one precedence/ownership pair per shim. No records falls back to the default location; unreadable/corrupt state fails diagnostics. No doctor flag or exported signature was added.

Unix process code targets macOS/Linux. Terminal detection reuses install's native ioctl via a tiny exported accessor. This integration changes no gh CLI parsing semantics; upstream behavior/source paths are the verified v2.102.0 findings in [native.md](native.md), [rest.md](rest.md), [graphql.md](graphql.md), [install.md](install.md), and [config-logging-redaction.md](config-logging-redaction.md).

## Verification

Final-fix run: root `gofmt -l .` printed nothing; `go build ./...`,
`go vet ./...`, `go test ./...`, `go test -race ./...`, and `make build`
passed on macOS arm64. Both full test commands reported 14 passing test
packages and nine packages without tests.

Hermetic E2E covers pre/post SIGINT/SIGTERM/SIGHUP interruption, resolver
cancellation, direct-helper disappearance (`kill(pid, 0) == ESRCH` within a
bounded wait), child-status preservation, inherited ignored INT/HUP, custom
doctor locations, and gh startup before stdin EOF for unneeded capture.
Body-dependent enforcing rules still capture and deny matching GraphQL input.
The built CLI also passed a standalone default-config streaming smoke:
fake gh started with the producer pipe open, received byte-exact input, emitted
no wrapper output, and logs retained uncaptured-input uncertainty. Management
normalize independently read the complete non-TTY input and proved its query
mutation-free.

Cross-compiled with `CGO_ENABLED=0 GOOS=linux GOARCH=arm64|amd64 go test -p=1 -c`
for all 14 tested packages; ordinary fuzz seeds are included, not timed fuzzing.
Prebuilt ghok/fakegh use the same Go 1.26.5 as the suite; E2E TestMain accepts
paired `GHOK_E2E_GHOK_BINARY` / `GHOK_E2E_FAKEGH_BINARY` paths, validates their
executability, and skips fixture compilation. Build ghok with
`-ldflags='-X main.version=e2e-version -X main.commit=e2e-commit'`.

Docker runs used existing images, `--pull=never --network=none --read-only`,
read-only binary mounts, UID/GID 502:20, and writable/executable `/tmp` tmpfs.
Every test binary ran with `-test.v -test.timeout=20m`; no skips:

| Linux runtime | Image | Result |
| --- | --- | --- |
| Native arm64 | `node:22-bookworm-slim` (Debian) | 13/14 packages passed, including full E2E. |
| Translated amd64 | `mcr.microsoft.com/powershell:7.4-ubuntu-22.04` | 14/14 packages passed, including full E2E. |

The arm64 `internal/process` package failed its unchanged
`TestShellForegroundBackgroundJobs`: `FG_DONE:149`, child acknowledged
`CHILD_DONE`, shell still listed the wrapper stopped by tty input. The failed
process suite was not rerun or suppressed; the three packages after it in the
initial loop were run separately and passed. No process implementation/test was
changed for this unrelated verification finding.

Linux race instrumentation was **not run**: no local golang image or established
Linux Go/C-compiler/sysroot combination was available. macOS amd64 runtime was
not exercised in this run. All temporary smoke/cross-test artifacts were
removed. Benchmark methods and both reported startup deltas remain in
[benchmarks.md](benchmarks.md).

