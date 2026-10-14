# CLI benchmarks

The benchmarks in `test/bench` run on supported Unix platforms (macOS and
Linux). They never invoke an installed `gh` or contact GitHub. `TestMain`
builds `cmd/ghok` and `test/fakegh` into a temporary directory only when
benchmarks are requested; normal `go test ./...` runs do not need those extra
fixture builds. All temporary binaries, configs, logs, and working directories
are removed when the benchmark process exits normally.

## Reproduce

From the repository root, with the pinned Go toolchain and dependencies already
available locally:

```sh
go test ./test/bench -run '^$' -bench . -benchmem -benchtime=2s -count=5
```

For a smaller measurement, select one family:

```sh
go test ./test/bench -run '^$' -bench '^BenchmarkStartup$' -benchmem -count=5
go test ./test/bench -run '^$' -bench '^BenchmarkNormalize$' -benchmem -count=5
go test ./test/bench -run '^$' -bench '^BenchmarkTrivialHook$' -benchmem -count=5
```

Builds occur before benchmark timers start. They use the normal Go module/build
caches, with module downloads, checksum-server lookups, and automatic toolchain
switching disabled (`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`). A missing
local dependency is a setup error, not a reason to download anything. The Go
compiler's caches are the only shared build infrastructure; measured fixture
processes receive a fresh, explicit environment rather than inheriting user
authentication or configuration.

Run on an otherwise idle machine, keep power/thermal conditions consistent, and
retain all samples rather than selecting the fastest. Record the command, Go
version, OS/architecture, CPU, memory, and revision alongside measured results.
The Go benchmark header reports the active CPU and concurrency suffix. Changing
`GOMAXPROCS`, enabling the race detector, or changing build flags produces a
different measurement and must be noted. These are warm repeated-process
measurements, not cold disk-cache or first-install latency measurements.

## What is measured

- **`BenchmarkStartup/DirectFakeGH`** starts the fake gh directly with
  `pr view 123 -R org/repo` and waits for it. This is the process-launch baseline.
- **`BenchmarkStartup/ShimNoConfig`** starts the same executable through the
  `gh -> ghok` shim, with the same arguments, environment, cwd, and empty stdin.
  No config file exists. Defaults enable logging, so this includes config
  discovery, normalization, observe-mode policy evaluation, and a local JSONL
  log write before execution. It is deliberately **not** the explicit
  `logging.enabled: false` direct-exec fast path. Normal rotation remains part
  of repeated default-logging runs. Subtract the paired direct baseline to
  estimate added wrapper latency; report both raw measurements as well.
- **`BenchmarkNormalize/L1NativeMerge`** normalizes a native admin/squash merge
  with an explicit repository.
- **`BenchmarkNormalize/L2RESTMerge`** normalizes the REST merge endpoint with
  an explicit PUT and merge method.
- **`BenchmarkNormalize/L3GraphQLMerge`** parses and analyzes a parameterized
  GraphQL `mergePullRequest` mutation. Node-ID repository resolution remains
  unresolved; no resolver or API is used. Each fixture is checked to produce
  exactly one `pr.merge` operation at the expected level before timing.
- **`BenchmarkTrivialHook`** runs one pre-hook through `hooks.Runner.Run`.
  The fake gh's hook mode reads the JSON request and responds with `abstain`.
  This includes protocol serialization, scheduling, process launch, pipes,
  response decoding, and waiting; it excludes hook selection, normalization,
  real gh execution, and the wrapper's overall startup.

Every benchmark calls `ReportAllocs`; output includes `ns/op`, `B/op`, and
`allocs/op`. Startup allocations describe the **benchmark parent process's**
`os/exec` machinery, not heap allocations inside either executable. Hook-runner
allocations are measured in-process, but the hook child's own heap is likewise
outside those counts. Normalization allocations describe the normalizer itself.

## Isolation

Measured children have temporary `HOME`, `XDG_CONFIG_HOME`, `GHOK_CONFIG_DIR`,
`GH_CONFIG_DIR`, `TMPDIR`, and cwd. Their PATH contains only the sandbox's shim
and fake gh directories; `GHOK_REAL_GH` explicitly pins that fake executable.
Git system/global configuration is disabled. No real gh, git executable,
credential helper, token, network service, user config, or user log directory is
needed. The trivial hook uses that same isolated environment plus
`FAKEGH_MODE=hook`; it writes no fixture record files.

## Measurement machine

Machine metadata collected for the integration run:

| Property | Value |
| --- | --- |
| OS / architecture | Darwin 25.5.0 / arm64 |
| CPU | Apple M4 Max |
| Logical CPUs | 16 |
| Memory | 64 GiB (68,719,476,736 bytes) |
| Go | go1.26.5 darwin/arm64 |

## Recorded measurements

Command: `go test ./test/bench -run '^$' -bench . -benchmem -benchtime=2s -count=5`.
All five samples passed (`ok github.com/mkusaka/ghok/test/bench 73.211s`).
Ordinary non-race fixture builds, default GOMAXPROCS=16, warm caches; host load
was not controlled. Revision label from the integration build: `f1b3523-dirty`.

| Benchmark | Five samples, ns/op | Median, ns/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| DirectFakeGH | 9960875, 8348257, 5801533, 5859729, 7617819 | 7617819 | 3400 | 32 |
| ShimNoConfig | 13529925, 16903131, 12863777, 14105531, 15193598 | 14105531 | 3400 | 32 |
| L1NativeMerge | 3263, 2715, 3356, 2834, 2738 | 2834 | 2416 | 26 |
| L2RESTMerge | 7249, 7153, 6805, 6992, 6742 | 6992 | 2218–2222 | 32 |
| L3GraphQLMerge | 6413, 5040, 7178, 5944, 9871 | 6413 | 5440 | 72 |
| TrivialHook | 5996270, 8021234, 7100623, 8547471, 8483517 | 8021234 | 78715–79202 | 114–118 |

The default-logging shim median was 14.106 ms versus 7.618 ms direct:
a difference of medians of 6.488 ms, not a controlled paired overhead estimate.
The explicit no-analysis/disabled-logging fast path was not benchmarked.
The broad startup spread makes these local measurements unsuitable as a
portable latency guarantee.
