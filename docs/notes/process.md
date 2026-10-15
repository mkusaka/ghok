# Process execution and stdin replay

## Integration

`Exec(Options) error` uses `syscall.Exec` with the supplied full argv (including argv[0]), exact environment, and current directory. `Env == nil` means empty environment. Capture or a live stdin replay pump requires `Run(context.Context, Options) Result`. Run/Exec own the supplied spool's cleanup; other callers must call `Spool.Close`, including when `SpoolStdin` returns a non-nil spool with an error.

`Result` exposes `ExitCode`, `Signal`, `CoreDump`, `Duration`, `StartErr`, `WaitErr`, and optional `Stdout`/`Stderr`. `Captured` exposes binary `Data`, total observed `Size`, `Truncated`, and `Error`. A zero capture limit retains no bytes but still tees/counts output. Capture errors never replace the child's exit status. `Result.Exit` must be called after post-hooks; it reproduces signaled termination, not a normal exit of 128+signal. Spawn failures map to 127 for ENOENT and 126 otherwise.

`SpoolStdin(*os.File, int64, time.Duration)` returns `*Spool`; `Data() model.StdinData`, `File() *os.File`, `RequiresSupervision() bool`, and `Close() error` are its public accessors. Negative byte limits are errors. Nonpositive timeouts mean no deadline. Methods are not concurrent; Run is the wrapper's sole child/signal/wait owner, not a general-purpose concurrent subprocess library.

## Signals, terminal, and job control

- Uncaptured stdout/stderr and unspooled stdin are inherited as file descriptors, with no Go copying goroutines. No child Setpgid, Setsid, or Foreground attribute is set.
- INT/QUIT are caught with Notify, never Ignore. They are forwarded to the child pid only when the wrapper is not the controlling terminal's foreground group. Foreground `kill -INT wrapper-pid`/`kill -QUIT wrapper-pid` therefore has an unavoidable ambiguity and is not forwarded. Terminal/group-directed INT/QUIT already reach the child once.
- TERM/HUP always forward to the child pid, not its process group. WINCH is not forwarded: terminal resize signals reach the foreground group naturally. USR1/USR2 forwarding is not implemented.
- TSTP/TTIN/TTOU notifications on the supervisor are caught, not forwarded. The child's actual wait stop is the suspension authority: a child that catches/ignores TSTP must not leave a stopped wrapper, and the wrapper must not stop before a terminal-reading child does. Wrapper-pid-only terminal-stop signals therefore do not suspend the job; signal the child or the job group.
- The main loop owns `wait4(WUNTRACED|WNOHANG)`, waking on SIGCHLD; no waiter goroutine retains a consumed stop across continuation. For a child stopped by TSTP/TTIN/TTOU, stop/continue notification delivery is joined/drained, the same signal is raised with default disposition on the locked current thread, terminal-stop notifications are re-armed, and the child receives CONT before another wait status is inspected. Re-arming before waking the child matters: a background reader can immediately generate a new group TTIN. This handoff also works when CONT notifications are explicitly ignored.
- Child state inspection remains a guard against independently continued children for terminal stops: Linux `/proc/<pid>/stat`, macOS `proc_pidinfo(PROC_PIDT_SHORTBSDINFO)`. Inspection failure conservatively mirrors the terminal stop. A child-pid SIGSTOP is not mirrored: the wrapper keeps waiting, so child-only SIGCONT permits reaping and post-hooks without a wrapper CONT. Uncatchable group-wide STOP cannot use the controlled terminal-stop handoff; arbitrary external STOP/CONT ordering is not guaranteed race-free.
- Go's BSD `syscall.WaitStatus.Stopped` classifies an actual SIGSTOP stop as Continued. Stop statuses are decoded directly so SIGSTOP is consumed without being mistaken for termination; WCONTINUED is not needed because child exit wakes the waiter through SIGCHLD.
- Context cancellation sends child CONT then TERM, allows one second, sends KILL if necessary, and always waits/reaps. Capture and stdin pumps are canceled/joined. Cancellation cannot act while the wrapper itself is stopped until it receives CONT.
- Grandchildren retain the group and naturally receive terminal signals. Wrapper-directed TERM/HUP goes only to the direct child; propagation/reaping of that child's descendants belongs to the child, as with a shell. The test helper verifies propagated TERM and grandchild reaping.
- Exit restores the actual default disposition and unblocks/raises the signal on the current thread: Linux kernel rt_sigaction/rt_sigprocmask/tgkill; macOS CGO-free libSystem sigaction/sigprocmask/raise calls with amd64/arm64 assembly address trampolines. RLIMIT_CORE is zeroed before raising, so the child's CoreDump diagnostic is preserved but the wrapper's core-dump wait flag/file need not match.

### Inherited ignored signals

Go 1.26 preserves inherited SIG_IGN for HUP and INT; these are checked with `signal.Ignored` before any Notify and left ignored. Explicit ignored dispositions set after runtime startup are also left untouched. The Go runtime replaces inherited ignored QUIT, TERM, PIPE, and most other signals during startup, before this package can inspect them. ForkExec resets Go-handled dispositions to default; Exec resets caught dispositions through execve. Exact pre-runtime SIG_IGN preservation for those signals is unavailable via the supported Go APIs and is not claimed. This is significant for a non-Go target launched from a shell that ignored QUIT/PIPE; a Go target independently applies the same runtime rules.

Sources inspected: Go 1.26.5 `src/runtime/signal_unix.go` (`initsig`, `sigInstallGoHandler`, `clearSignalHandlers`, `dieFromSignal`), `src/runtime/proc.go` (`syscall_runtime_AfterForkInChild`), `src/runtime/os_darwin.go`, `src/syscall/exec_unix.go`, `src/syscall/exec_libc.go`, `src/syscall/syscall_bsd.go`, and `src/syscall/syscall_darwin.go`. Darwin process-state ABI: SDK `sys/proc_info.h`, `sys/proc.h`, and `sys/param.h`. No cli/cli source was needed for process control.

Controlled handoff sources inspected: Go 1.26.5 `src/os/signal/signal.go` (`Notify`, `Stop` and delivery quiescence), `src/runtime/sigqueue.go` (asynchronous signal delivery), and `src/runtime/signal_unix.go` (`sigenable`, `sigdisable`, default-action restoration). Darwin `kill(2)` documents process versus process-group addressing.

### Implementation risk

Darwin signal reset uses `//go:linkname syscall.syscall` and CGO-free `cgo_import_dynamic` assembly trampolines into libSystem. Plain `signal.Reset` plus `kill` is insufficient: Go can retain runtime handlers, including Darwin's handling of SIGPIPE, rather than install SIG_DFL. A future Go release could restrict this linkname and require updating the platform implementation. If default-disposition restoration fails or raising the signal leaves the wrapper alive, `Exit` falls back to a normal exit of `128+n`; that does not reproduce WIFSIGNALED.

## Optional capture consequences

Capture is off by default. Separate child pipes are read in chunks, immediately written to the original output destinations, and retain only bounded prefixes. Binary output is unchanged. EPIPE closes that stream's pipe reader instead of silently draining, allowing the child to observe SIGPIPE/EPIPE. Duplicated tee descriptors are above fd 2, avoiding Go's wrapper-side special fd-1/fd-2 SIGPIPE termination.

The child sees non-TTY stdout/stderr: gh colors, formatting, and pager decisions can differ. Cross-stream ordering can change. Backpressure is maintained; capture waits for pipe EOF, including writers retained by descendants. Context cancellation aborts that wait. No GH_FORCE_TTY or other environment variable is injected. Tee cancellation temporarily uses O_NONBLOCK on duplicated output descriptors; this flag is shared with the original open-file description and can be visible to other processes sharing it. Both initial flag sets are saved before either change and restored after both pumps join. A kernel-stalled regular-file/device write cannot be interrupted portably with Go file deadlines.

## Stdin

TTY stdin is never read. Regular files are inspected from their current offset up to the limit plus one distinguishing byte, then restored to that offset; no temporary replay file is needed. Other inputs use an immediately unlinked, 0600 temporary file. EOF within the limit permits file-backed Exec replay. Timeout or oversized input reports no analysis bytes but preserves every consumed byte; Run pumps the prefix followed by live input through a child pipe. Raw readiness waits plus a cancellation pipe avoid leaked blocking reader goroutines, including redirected inherited stdin that does not support Go deadlines. Sole-reader ownership of stdin is required.

Linux uses ppoll; macOS uses poll. macOS FIFO EOF readiness can be absent even after the writer closes, so FIFO reads additionally make brief nonblocking probes with immediately restored original flags and bounded 20ms readiness waits. This applies to both capture and live replay. A nonblocking read returning zero is actual EOF, not a reason to wait for hypothetical future writers. The Go `src/os/file_unix.go` implementation independently documents Darwin FIFO readiness limitations and inherited blocking descriptors' deadline limitations.

A spool changes pipe-backed child stdin to a regular file after complete capture, or another pipe when pumping. Programs that inspect stdin descriptor type can observe that opt-in analysis step. Concurrent modification of a redirected regular input file is not snapshotted.

## Verification surface

Real wrapper/child subprocess tests cover argv/environment/cwd, descriptor hygiene, normal and signaled wait status, spawn failures, binary/empty/large streams, SHA-256 of 64 MiB output, streaming, broken pipes, directed/terminal signals, inherited HUP/INT ignore, cancellation/escalation, grandchild reaping, stop/continue, PTY TTY/resize/Ctrl-C/Ctrl-\\, and an interactive bash Ctrl-Z/bg/fg scenario. Bash absence is the only conditional skip. Spool tests cover file offsets, binary/empty inputs, limits, timeout/live replay, FIFOs/sockets/read errors, secure unlinked files, and cancellation/cleanup. Benchmarks compare supervised/direct true and file/pipe spooling.

PTY fixtures use nonblocking, runtime-pollable masters so closing them interrupts pending reads. Cleanup closes the master before bounded kill/reap; normal completion drains output while waiting. Each subprocess has one retained waiter shared by normal completion and cleanup. Stop observation uses bounded WNOHANG waits before starting that waiter. Shell job-control checks disable echo/line editing, accept only exact result markers, and report actual completion plus `jobs -l` output on failure.

External smoke invokes Exec and Run with 1 MiB binary stdin, native shell SIGTERM, and installed gh 2.102.0 `--version` under isolated HOME/XDG/PATH; output/status matches direct gh. Required build/vet/whole-repository tests and the process race suite pass on macOS arm64. Complete compiled process suites also pass in existing, network-disabled Linux containers: native arm64 and Rosetta-translated amd64. macOS amd64 is cross-compiled, not runtime-tested; Linux race instrumentation was not run locally. Loader-owned executable image descriptors are reported separately from process-owned fd hygiene assertions because Rosetta retains an extra image fd at each exec boundary. No test skipped in the exercised environments.

Review robustness check: `go test -race -count=5 -timeout=900s ./internal/process/` and `go test -count=3 -timeout=600s ./...` pass concurrently with a separate process running `runtime.NumCPU()` busy-loop goroutines throughout verification. The unread-PTY-output cleanup and blocked-pipe/repeated-reap regressions pass; formatting, build, vet, and complete Linux arm64/translated-amd64 suites pass.

### 2026-10-15 job-control race repair

The old child-state check could observe a child still stopped while the shell's group CONT had already resumed the wrapper. Mirroring that obsolete wait event stopped the foreground wrapper again. Stopping on the wrapper's own terminal notification is also wrong: the child may not have stopped yet and can consume shell input. The controlled handoff above uses the child's actual stop, not notification timing or a sleep.

| `TestShellForegroundBackgroundJobs` | Before repair | Final implementation |
| --- | --- | --- |
| Linux arm64, existing `node:22-bookworm-slim` | 195 passed / 5 failed, 200 runs | 1,000 passed / 0 failed |
| macOS arm64, 16 concurrent `/usr/bin/yes` CPU-load processes | 192 passed / 8 failed, 200 runs | 1,000 passed / 0 failed |

Stress uses compiled process test binaries with `-test.run='^TestShellForegroundBackgroundJobs$' -test.count=200` before and `-test.count=1000` after, plus `-test.v -test.timeout=20m`. Linux builds use `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`; Docker uses `--pull=never --network=none --read-only --platform=linux/arm64`, a read-only suite mount, and writable `/tmp` tmpfs. Each outer stress command has a bounded deadline; all CPU-load processes are joined afterward.

Regression fixtures explicitly disable CONT notifications in the wrapper and verify that wrapper-only CONT resumes a child-pid terminal stop for TSTP/TTIN/TTOU. A separate child catches TSTP and completes without stopping; its wrapper must also complete. No product ordering hook, sleep widening, dependency, exported API, or shared-contract change is needed.

Both deterministic regressions fail against the HEAD supervisor via a Go `-overlay`, without modifying the working product source: the handled-TSTP wrapper and the CONT-notification-disabled child-stop wrapper remain stopped until the fixture's 15-second watchdog kills them. The fixed supervisor passes both scenarios.

Final Linux bundles cross-compile every package with tests using `CGO_ENABLED=0 GOOS=linux GOARCH=arm64` or `amd64` and `go test -p=1 -c`, plus prebuilt `ghok`/`fakegh` for `GHOK_E2E_GHOK_BINARY` and `GHOK_E2E_FAKEGH_BINARY`. All 14 test packages pass, with zero skips, on native arm64 and translated amd64 in the existing arm64 Node image. Each binary uses `-test.v -test.timeout=20m` with an outer `timeout 1200`; Docker remains network-disabled/read-only with `/tmp` tmpfs. Linux race instrumentation is not locally exercised. Temporary binaries, runners, diagnostic logging, and the HEAD overlay are removed.

Final macOS arm64 checks pass: `gofmt -l .` (empty), `go build ./...`, `go vet ./...`, `go test -count=1 -timeout=20m ./...`, `go test -race -count=1 -timeout=20m ./...`, and `go test -race -count=1 -timeout=20m ./internal/process/...`. Both whole-repository suites report 14 successful packages and nine without tests; the explicit process race suite ends `ok github.com/mkusaka/ghok/internal/process 77.862s`.

### 2026-10-15 child-only SIGSTOP/CONT

Mirroring a child's SIGSTOP stopped the wrapper even though only the child was targeted. Continuing only the child left the wrapper stopped, unable to reap or run post-hooks. Only terminal stops now enter the existing controlled suspension handoff; SIGSTOP is consumed while the wrapper continues waiting.

`TestChildSIGSTOPContinueDoesNotStopWrapper` fails before the change with `wrapper mirrored child-only SIGSTOP`, both with normal and ignored wrapper CONT notifications. After the change, both cases verify that the child stays stopped while the wrapper does not, child-only CONT lets Run return an exit-zero Result, and the child PID is gone rather than left as a zombie. Existing terminal-stop and handled-TSTP regressions pass on macOS arm64 and offline Linux arm64.

Verification for this change:

- `go test -race -count=1 -timeout=20m ./internal/process`: passed on macOS arm64 (`78.212s`).
- `TestShellForegroundBackgroundJobs`: 200 passed / 0 failed on macOS arm64 with 16 concurrent `/usr/bin/yes` load processes, all killed/joined afterward; 200 passed / 0 failed on Linux arm64 using the existing `node:22-bookworm-slim` image with `--pull=never --network=none --read-only`, a read-only suite mount, and `/tmp` tmpfs.
- `gofmt -l internal/process`: empty. golangci-lint v2.14.0 `run ./internal/process/...`: 0 issues both natively and with `CGO_ENABLED=0 GOOS=linux`.
- Actual built-wrapper smoke with an isolated fake gh and post-hook: external STOP targets only the child (`TN`), wrapper remains unstopped (`SNs`), external CONT targets only the child, wrapper exits 23, post-hook receives `result.exitCode: 23`, and the child PID no longer exists. No GitHub access is involved.

