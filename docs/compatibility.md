# Compatibility and transparency

Supported release targets: **macOS and Linux, amd64 and arm64**. The command,
flag, API-field, alias and resolver baseline is **GitHub CLI 2.102.0**, with Go
1.26.5 recorded in go.mod. ghok does not fork gh or alter its authentication.
New/unknown syntax remains uncertain (a known native command can retain its
identity with heuristic confidence); a doctor version check is informational,
not certification of all older/newer gh releases.

## Platform matrix and verification scope

| OS | Architecture | CI runner | Release binary |
| --- | --- | --- | --- |
| Linux | amd64 | `ubuntu-latest` | `linux_amd64` |
| Linux | arm64 | `ubuntu-24.04-arm` | `linux_arm64` |
| macOS | arm64 | `macos-latest` | `darwin_arm64` |
| macOS | amd64 | `macos-15-intel` | `darwin_amd64` |

These labels are listed in the [GitHub-hosted runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners),
checked 2026-10-15. `macos-15-intel` explicitly selects Intel; `macos-latest`
currently selects arm64. CI verifies Go's native OS/architecture so a future
label change fails rather than silently losing coverage. Every matrix job runs
vet, formatting, tests, race tests, five 10-second fuzz smokes, make build, and
all four CGO-free cross-compiles. Cross-build success alone is not runtime proof.

Engineering records report macOS arm64 runtime/race/E2E/PTY tests and Linux
arm64 plus translated amd64 non-race runtime suites. The reported Linux
job-control race was repaired; 1,000 iterations pass on Linux arm64 and on
macOS arm64 under CPU load (see [process verification](notes/process.md#2026-10-15-job-control-race-repair)). macOS amd64 was previously
cross-compiled, not locally executed. The native CI jobs are the ongoing
verification surface, not a claim that those hosted jobs have already run.
No minimum Linux distribution or macOS version beyond these exercised/CI
surfaces is certified. Apple release binaries are not Developer ID
signed/notarized. Windows and other operating systems are unsupported.

## Execution paths

- No selected delegate/post-hooks and no live stdin replay: `syscall.Exec` replaces ghok.
  After pre-checks the real gh owns pid/job/streams directly. Logging precedes
  exec and has no final child outcome.
- Post-hooks or live replay: ghok supervises one child in the **same process
  group**. No Setpgid/Setsid/Foreground is set for real gh. Default descriptors
  are inherited; the wrapper does not emulate a terminal.
- A selected delegate uses the same supervisor, cwd and signal behavior as real
  gh, but always streams stdout/stderr through output-tracking pipes. Non-TTY
  stdin is retained live for fallback; TTY stdin is inherited and cannot be
  rewound. Exit 125 without stdout or default-handled start failure/empty-output
  timeout runs real gh once; other exits/signals pass through. See the full
  [delegate contract](delegates.md) for timeout, replay and non-TTY output limits.
- Hook commands have separate process groups, JSON stdin and captured output;
  they are trusted, non-interactive programs, not terminal-transparent gh jobs.

## Signal, TTY, and job-control behavior

| Surface | Behavior | Status / limit |
| --- | --- | --- |
| Original command arguments | Analysis may expand aliases; shim execution preserves argv[0]; `ghok exec` forces argv[0] to `gh` and accepts all following arguments verbatim (`--` optional, no args allowed). | Original argv preserved unless a trusted transform changes the request; changed requests use expanded argv. |
| Environment/cwd | Original cwd/environment plus incremented GHOK_DEPTH; GH_PATH is not rewritten. Trusted transform env edits affect gh and POST, not PRE process environments. | Intentional metadata and explicitly configured transform edits. |
| Normal child exit | Original 0–255 status returned after post-hooks. | Preserved; pre-launch content denial is ghok exit 77, hook/config error denial 78. |
| Spawn failure | ENOENT -> 127; other execution failures -> 126. Discovery failure -> 127. | Shell-style convention. |
| Uncaptured stdout/stderr; unspooled stdin | Original descriptors inherited. Wrapped `gh --version` / `gh version` adds a ghok version/path line only on terminal stdout. | Other binary/empty/large output unchanged; piped version output unchanged. |
| Interactive TTY and size | Real gh remains in shell's group; no PTY substitution. Resize reaches foreground group naturally. | Preserved without capture. |
| Terminal/group SIGINT/SIGQUIT | Child receives terminal signal once. Foreground supervisor catches its own notification without forwarding it again. | Tested exactly-once terminal delivery. |
| SIGINT/SIGQUIT directed only at wrapper pid while foreground | Cannot distinguish terminal notification from directed kill reliably; not forwarded. | **Not exactly transparent**; signal child/group instead. |
| SIGINT/SIGQUIT when supervisor is not terminal foreground | Forwarded to direct child pid. | Supported nonforeground/non-TTY forwarding. |
| Wrapper-directed SIGTERM/SIGHUP while gh runs | Forwarded to direct child pid. | No process-group broadcast; descendants must be handled by child. |
| SIGWINCH | Not forwarded; foreground terminal group receives it naturally. | Pid-only WINCH sent to wrapper is not relayed. |
| SIGTSTP/SIGTTIN/SIGTTOU | Child's actual stop controls suspension; wrapper catches its group notification, then raises the same default-action stop only after the child stops. | Shell fg/bg supported; wrapper-pid-only terminal stops are not relayed. A child that handles/ignores the stop does not stop the wrapper. |
| Child-pid SIGSTOP | Wrapper keeps waiting without suspending itself; child-pid-only SIGCONT permits reaping and post-hooks. | No wrapper CONT required. Group-wide SIGSTOP still stops the wrapper unconditionally. |
| SIGCONT | On return from controlled suspension, re-arm terminal-stop handling and continue the child before inspecting another wait status; other wrapper CONT notifications are forwarded. | Terminal-stop handoff supports ignored CONT notifications; cancellation cannot progress while wrapper is stopped. Uncatchable group-wide STOP/CONT ordering is outside the controlled terminal-stop guarantee. |
| Child termination by signal | After post-hooks, install default disposition/unblock/raise same signal on wrapper. | Shell sees signaled termination, not merely normal exit 128+n, when platform restoration succeeds. |
| Core-dump status | Child core flag retained in diagnostics; wrapper RLIMIT_CORE zeroed before re-raising. | Wrapper core-dump wait flag/file may differ from child's. |
| Signal-restoration failure | Fallback normal exit 128+n. | Exceptional fallback is not WIFSIGNALED-equivalent. |
| Inherited ignored HUP/INT | Go preserves these, and execution checks them before Notify. | Preserved. |
| Inherited ignored QUIT/TERM/PIPE and most others | Go runtime may replace them before ghok can inspect. Exec/ForkExec reset caught signals. | Exact pre-runtime ignored dispositions **not supported**. |
| SIGPIPE / broken output pipe | Uncaptured child uses its fd semantics; capture closes that pipe reader on EPIPE so child sees SIGPIPE/EPIPE. | Child status preserved, subject to Go disposition limitation above. |
| USR1/USR2 | No wrapper-directed forwarding. | Unsupported supervised relay. |
| SIGINT/SIGTERM/SIGHUP during resolver calls or pre-hooks | Cancel/reap helper groups; no user gh start, then re-raise the received signal. | Startup-ignored signals are not watched, subject to the Go disposition limitation above. |
| SIGINT/SIGTERM/SIGHUP during post-hooks | Cancel/reap hooks, log interruption, return gh's completed exit/signal. | Hook interruption never replaces gh status. |
| Post-hooks | Failures/decisions diagnostic only. | Never replace gh exit status. |
| Abnormal wrapper SIGKILL | Cannot run post-hooks or cleanup; hooks may outlive ghok. | SIGKILL cannot be intercepted; no escaped-descendant guarantee. |

Context cancellation in the process API sends child CONT/TERM, waits one second,
then KILL if needed, always reaping the direct child. The CLI uses ordinary
signal forwarding during real-gh execution, not a timeout on the user's gh
command. Hook timeouts use TERM/KILL on their separate group after 500 ms;
escaped descendants are outside that guarantee.

One helper-phase watcher covers resolver API calls and pre-hooks, is stopped
and joined before Exec/Run takes ownership, and restarts after Run returns for
post-hooks. Helper runners kill their separate process groups on context
cancellation and reap direct children before the wrapper exits.

## Opt-in output capture

A selected post-hook with captureOutput enables separate child stdout/stderr
pipes. Delegates also require these pipes to track safe fallback, but retain
output bodies only when a post-hook requests capture. Output is streamed to the original destinations and bounded prefixes
are included as redacted base64 with total size/truncation metadata. A selected
noncapturing post-hook does not receive bodies even if another hook requested
capture. Logs always omit those bodies.

**Partial transparency:** child outputs are non-TTY, so colors/pager/formatting
may differ; cross-stream ordering may change. Backpressure remains. Descendants
holding write ends can delay EOF after child exit. Canceling tee temporarily
sets O_NONBLOCK on duplicated output descriptors, sharing the flag with their
original open-file descriptions, then restores original flags after pumps
join. Kernel-stalled file/device writes cannot always be interrupted portably.
No GH_FORCE_TTY variable is injected to mask this difference.

## API stdin analysis

Only analyzable alias-expanded API `--input -` or typed `-F key=@-` consumers
trigger capture, and only if a potentially relevant enforce-mode deny rule,
unknown-mutation/protected-repository denial, enabled pre-hook/transform, or
configured active delegate can need it.
Command prefix and normalization level can exclude a candidate; body-dependent
action/effect/target matches are treated conservatively before capture.
Observe logging, audit-only policies, and post-hooks alone never trigger capture:
gh inherits stdin immediately, and analysis/logs report `stdin not captured`.
Normalize/explain inspection may still read non-TTY stdin regardless of policy.
Interactive stdin is never pre-read.
The default spool/file analysis limits are 8 MiB and stdin timeout 3 seconds;
GraphQL has its own 1 MiB request/query limit. Parser failure/unknown root-flag
API dispatch may prevent stdin analysis; see [normalization](normalization.md).

- Regular files are read from their current offset and restored; concurrent
  file changes are not snapshotted.
- Pipes/FIFOs/sockets use an immediately unlinked 0600 spool. EOF within bounds
  permits exec with file-backed replay; oversized/timeout input preserves its
  consumed prefix and uses supervised prefix-plus-live-pipe replay.
- Analysis failure exposes no incomplete analysis bytes as a complete request.
  Policy decides unknown handling; replay uses original bytes, never redacted
  display copies. If consumed bytes cannot be preserved, execution fails rather
  than silently dropping them.
- Complete pipe capture changes child stdin from pipe to regular file;
  descriptor-inspecting programs can observe it. Sole-reader ownership is
  required. Multiple GraphQL stdin consumers are not modeled exactly.

## macOS versus Linux

| Implementation detail | macOS | Linux |
| --- | --- | --- |
| TTY inspection | Native TIOCGETA/TIOCGWINSZ ioctl | Native TCGETS/TIOCGWINSZ ioctl |
| Child stopped-state check | proc_pidinfo SHORTBSDINFO | /proc/<pid>/stat |
| Stdin readiness | poll; brief nonblocking FIFO EOF probes, restored flags | ppoll |
| Default signal restore/raise | CGO-free libSystem calls via architecture assembly trampolines/linkname | Kernel rt_sigaction/rt_sigprocmask/tgkill |
| Path canonicalization | /var and /tmp may alias /private paths | Ordinary symlink canonicalization |
| Logs/ownership/process groups | Unix flock, secure files, POSIX groups | Same semantics assumed on local filesystem |

Darwin's signal implementation depends on Go/linker internals: future toolchain
changes can require an implementation update. Source analysis and tests are in
[process notes](notes/process.md). Doctor's capability checks are diagnostics,
not process-control certification. Network-filesystem flock behavior is not
promised.

## Explicit partial/unsupported features

- Windows/other OSes; interactive hooks; arbitrary process-group escape cleanup.
- Full gh server semantics or proof of successful side effects; normalization
  is intent/request analysis only. Registry drift on other gh versions.
- Shell-alias/extension internals; unknown future commands/flags/routes/mutations.
- Complete local git/SSH configuration equivalence: no global/system git
  config, conditional includes, URL insteadOf, reftable, full ssh -G,
  authenticated-host filtering, or interactive fork discovery. Resolver keeps
  uncertainty rather than certifying a guessed target.
- All supervised signal/disposition/capture edge cases described above.
- Security-boundary enforcement, tamper-proof audit logging, and arbitrary
  encoded-secret redaction. [Security](security.md) defines the trust limits.

Verified upstream behavior and fixtures remain in [native](notes/native.md),
[REST](notes/rest.md), [GraphQL](notes/graphql.md), [resolver](notes/resolve.md),
[installation](notes/install.md), [process](notes/process.md), and
[CLI](notes/cli.md) notes. These records distinguish source-inspected behavior,
local runtime tests, cross-compilation, and not-yet-exercised platform cases.
