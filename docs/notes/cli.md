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

SIGINT/SIGTERM during pre-hooks cancel and wait for hook process-group cleanup, do not start gh, then reproduce signaled termination through `process.Result.Exit`. The pre-hook signal watcher is stopped/joined before the execution layer owns signals. Inherited ignored SIGINT remains ignored. Child execution/job-control/capture limitations are exactly those documented in [process.md](process.md), including foreground wrapper-directed INT/QUIT ambiguity and opt-in capture's non-TTY streams.

## Analysis and resolution

Stdin analysis is only attempted for alias-expanded `gh api --input -` / `-F key=@-` when analysis is needed. The existing spool never reads a TTY and preserves consumed bytes, including oversized/timeout prefixes. Files are read relative to cwd with `runtime.fileMaxBytes` bounds. Analysis copies are never replay data.

Local git/host resolution runs only for unresolved repository/host matches or potentially writing operations whose protected-repository membership is unresolved. API resolution is permitted only in enforce mode for missing fields relevant to a potentially matching pre-hook/deny rule or protected-repository decision. Each operation is resolved separately so an unrelated operation does not trigger an API query. Resolver timeouts and ambiguity remain explicit diagnostics/unresolved fields. Observe mode never implicitly calls the API. Management API resolution requires explicit `--resolve --allow-api`.

Hook input and logger records cross `redact.New(env)` boundaries. gh still receives original secrets and bytes. Logging errors never change execution and appear only with `GHOK_DEBUG=1`. Hooks are trusted executable code; ghok is not a security boundary, and direct gh/API access bypasses it.

## Management and platform

Management commands use stdlib flag parsing. Success is 0, failure is 1, usage errors are 2. Explain exits 0 even for a displayed deny; it does not run hooks or gh. Hooks test runs selected hooks with synthetic execution metadata for post phase, never executes the requested gh command. Enable/disable manage only owned shims/state and never edit shell rc files. Normalization JSON and explanations redact secrets unless explain explicitly requests `--show-secrets`.

Unix process code targets macOS/Linux. Terminal detection reuses install's native ioctl via a tiny exported accessor. This integration changes no gh CLI parsing semantics; upstream behavior/source paths are the verified v2.102.0 findings in [native.md](native.md), [rest.md](rest.md), [graphql.md](graphql.md), [install.md](install.md), and [config-logging-redaction.md](config-logging-redaction.md).

## Verification

Root `gofmt -l .`, `go build ./...`, `go vet ./...`, `go test ./...`,
`go test -race ./...`, and `make build` passed on macOS arm64. An initial
hook-list test incorrectly decoded display JSON into YAML configuration types;
it now checks the public list fields. Concurrent cold test execution also
exceeded the existing installation diagnostic's three-second version-probe
deadline; E2E fixture compilation uses one compiler worker, and the final root
race run passed without changing that production timeout.

Hermetic E2E covers original argv/env/cwd, binary/empty/large stdio, fast and
supervised execution, actual signal wait statuses, 126/127 start failures,
pre-denial/allow/error/timeout, post diagnostics/status/captures, stdin limits
and live replay, GraphQL stdin/files, observe/protected policy, lazy resolution,
aliases/nesting/recursion, config passthrough, credential-safe logs/hook input,
management commands, and PTY interactive stdin/resize/exactly-once terminal Ctrl+C.

All cross-compiled test suites also passed with no skips in fresh,
network-disabled cached Linux containers: native arm64 (Debian) and
emulated amd64 (Ubuntu). Tests used actual CGO-free ghok/fakegh
binaries; a temporary compiler adapter copied already cross-built fixtures
for E2E TestMain because those containers contain no Go toolchain. Repository
mounts were read-only. Linux race instrumentation was not run. Linux
amd64/arm64 and macOS amd64 release builds cross-compiled successfully;
macOS amd64 runtime execution was not performed.

Manual sandbox smoke exercised enable → `gh pr merge 1` denied by a pre-hook
(exit 1, no fake-gh record) → disable. Go/Python example hooks both denied
admin=true and abstained for admin=false; the Go audit example appended one
JSONL record with exitCode=42 at its default installed path. All temporary
smoke/cross-test fixtures were removed. Measured benchmarks are in
[benchmarks.md](benchmarks.md).
