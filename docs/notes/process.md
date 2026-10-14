# Process execution and stdin replay

## Integration

`Exec(Options) error` uses `syscall.Exec` with the supplied full argv (including argv[0]), exact environment, and current directory. `Env == nil` means empty environment. Capture or a live stdin replay pump requires `Run(context.Context, Options) Result`. Run/Exec own the supplied spool's cleanup; other callers must call `Spool.Close`, including when `SpoolStdin` returns a non-nil spool with an error.

`Result` exposes `ExitCode`, `Signal`, `CoreDump`, `Duration`, `StartErr`, `WaitErr`, and optional `Stdout`/`Stderr`. `Captured` exposes binary `Data`, total observed `Size`, `Truncated`, and `Error`. A zero capture limit retains no bytes but still tees/counts output. Capture errors never replace the child's exit status. `Result.Exit` must be called after post-hooks; it reproduces signaled termination, not a normal exit of 128+signal. Spawn failures map to 127 for ENOENT and 126 otherwise.

`SpoolStdin(*os.File, int64, time.Duration)` returns `*Spool`; `Data() model.StdinData`, `File() *os.File`, `RequiresSupervision() bool`, and `Close() error` are its public accessors. Negative byte limits are errors. Nonpositive timeouts mean no deadline. Methods are not concurrent; Run is the wrapper's sole child/signal/wait owner, not a general-purpose concurrent subprocess library.

## Signals, terminal, and job control

- Uncaptured stdout/stderr and unspooled stdin are inherited as file descriptors, with no Go copying goroutines. No child Setpgid, Setsid, or Foreground attribute is set.
- INT/QUIT are caught with Notify, never Ignore. They are forwarded to the child pid only when the wrapper is not the controlling terminal's foreground group. Foreground `kill -INT wrapper-pid`/`kill -QUIT wrapper-pid` therefore has an unavoidable ambiguity and is not forwarded. Terminal/group-directed INT/QUIT already reach the child once.
- TERM/HUP always forward to the child pid, not its process group. WINCH is not forwarded: terminal resize signals reach the foreground group naturally. USR1/USR2 forwarding is not implemented.
- TSTP/TTIN/TTOU retain their default actions. Wait4 uses WUNTRACED; a genuinely stopped child causes the wrapper to stop itself with the same signal. CONT to the wrapper continues the child. Current process state is checked before mirroring a stop, avoiding replay of an obsolete stop notification after a shell fg/bg transition. Linux uses `/proc/<pid>/stat`; macOS uses `proc_pidinfo(PROC_PIDT_SHORTBSDINFO)`. If state inspection fails, the genuine stop is conservatively mirrored.
- Go's BSD `syscall.WaitStatus.Stopped` classifies an actual SIGSTOP stop as Continued. Stop statuses are decoded directly; WCONTINUED is not requested.
- Context cancellation sends child CONT then TERM, allows one second, sends KILL if necessary, and always waits/reaps. Capture and stdin pumps are canceled/joined. Cancellation cannot act while the wrapper itself is stopped until it receives CONT.
- Grandchildren retain the group and naturally receive terminal signals. Wrapper-directed TERM/HUP goes only to the direct child; propagation/reaping of that child's descendants belongs to the child, as with a shell. The test helper verifies propagated TERM and grandchild reaping.
- Exit restores the actual default disposition and unblocks/raises the signal on the current thread: Linux kernel rt_sigaction/rt_sigprocmask/tgkill; macOS CGO-free libSystem sigaction/sigprocmask/raise calls with amd64/arm64 assembly address trampolines. RLIMIT_CORE is zeroed before raising, so the child's CoreDump diagnostic is preserved but the wrapper's core-dump wait flag/file need not match.

### Inherited ignored signals

Go 1.26 preserves inherited SIG_IGN for HUP and INT; these are checked with `signal.Ignored` before any Notify and left ignored. Explicit ignored dispositions set after runtime startup are also left untouched. The Go runtime replaces inherited ignored QUIT, TERM, PIPE, and most other signals during startup, before this package can inspect them. ForkExec resets Go-handled dispositions to default; Exec resets caught dispositions through execve. Exact pre-runtime SIG_IGN preservation for those signals is unavailable via the supported Go APIs and is not claimed. This is significant for a non-Go target launched from a shell that ignored QUIT/PIPE; a Go target independently applies the same runtime rules.

Sources inspected: Go 1.26.5 `src/runtime/signal_unix.go` (`initsig`, `sigInstallGoHandler`, `clearSignalHandlers`, `dieFromSignal`), `src/runtime/proc.go` (`syscall_runtime_AfterForkInChild`), `src/runtime/os_darwin.go`, `src/syscall/exec_unix.go`, `src/syscall/exec_libc.go`, `src/syscall/syscall_bsd.go`, and `src/syscall/syscall_darwin.go`. Darwin process-state ABI: SDK `sys/proc_info.h`, `sys/proc.h`, and `sys/param.h`. No cli/cli source was needed for process control.

## Optional capture consequences

Capture is off by default. Separate child pipes are read in chunks, immediately written to the original output destinations, and retain only bounded prefixes. Binary output is unchanged. EPIPE closes that stream's pipe reader instead of silently draining, allowing the child to observe SIGPIPE/EPIPE. Duplicated tee descriptors are above fd 2, avoiding Go's wrapper-side special fd-1/fd-2 SIGPIPE termination.

The child sees non-TTY stdout/stderr: gh colors, formatting, and pager decisions can differ. Cross-stream ordering can change. Backpressure is maintained; capture waits for pipe EOF, including writers retained by descendants. Context cancellation aborts that wait. No GH_FORCE_TTY or other environment variable is injected. Tee cancellation temporarily uses O_NONBLOCK on duplicated output descriptors; this flag is shared with the original open-file description and can be visible to other processes sharing it. Both initial flag sets are saved before either change and restored after both pumps join. A kernel-stalled regular-file/device write cannot be interrupted portably with Go file deadlines.

## Stdin

TTY stdin is never read. Regular files are inspected from their current offset up to the limit plus one distinguishing byte, then restored to that offset; no temporary replay file is needed. Other inputs use an immediately unlinked, 0600 temporary file. EOF within the limit permits file-backed Exec replay. Timeout or oversized input reports no analysis bytes but preserves every consumed byte; Run pumps the prefix followed by live input through a child pipe. Raw readiness waits plus a cancellation pipe avoid leaked blocking reader goroutines, including redirected inherited stdin that does not support Go deadlines. Sole-reader ownership of stdin is required.

Linux uses ppoll; macOS uses poll. macOS FIFO EOF readiness can be absent even after the writer closes, so FIFO reads additionally make brief nonblocking probes with immediately restored original flags and bounded 20ms readiness waits. This applies to both capture and live replay. A nonblocking read returning zero is actual EOF, not a reason to wait for hypothetical future writers. The Go `src/os/file_unix.go` implementation independently documents Darwin FIFO readiness limitations and inherited blocking descriptors' deadline limitations.

A spool changes pipe-backed child stdin to a regular file after complete capture, or another pipe when pumping. Programs that inspect stdin descriptor type can observe that opt-in analysis step. Concurrent modification of a redirected regular input file is not snapshotted.

## Verification surface

Real wrapper/child subprocess tests cover argv/environment/cwd, descriptor hygiene, normal and signaled wait status, spawn failures, binary/empty/large streams, SHA-256 of 64 MiB output, streaming, broken pipes, directed/terminal signals, inherited HUP/INT ignore, cancellation/escalation, grandchild reaping, stop/continue, PTY TTY/resize/Ctrl-C/Ctrl-\\, and an interactive bash Ctrl-Z/bg/fg scenario. Bash absence is the only conditional skip. Spool tests cover file offsets, binary/empty inputs, limits, timeout/live replay, FIFOs/sockets/read errors, secure unlinked files, and cancellation/cleanup. Benchmarks compare supervised/direct true and file/pipe spooling.

External smoke invokes Exec and Run with 1 MiB binary stdin, native shell SIGTERM, and installed gh 2.102.0 `--version` under isolated HOME/XDG/PATH; output/status matches direct gh. Required build/vet/whole-repository tests and the process race suite pass on macOS arm64. Complete compiled process suites also pass in existing, network-disabled Linux containers: native arm64 and Rosetta-translated amd64. macOS amd64 is cross-compiled, not runtime-tested; Linux race instrumentation was not run locally. Loader-owned executable image descriptors are reported separately from process-owned fd hygiene assertions because Rosetta retains an extra image fd at each exec boundary. No test skipped in the exercised environments.
