# Installation

## Caller contract

`FindRealGh` takes explicit PATH, Self, and an optional absolute override. The caller selects config `runtime.realGh` / `GHOK_REAL_GH` precedence. It returns the stable PATH-facing gh path, not a Cellar target, and reports all candidates including duplicates. Each call re-resolves links; no cache or binary-content reads. SameFile and resolved basename `ghok` exclude wrappers. Missing/broken/looping links, nonregular/nonexecutable files, and relative/empty PATH entries are rejected with reasons. Invalid overrides fail without fallback.

`NextDepth` preserves its input and other variables, including GH_PATH. It emits one incremented GHOK_DEPTH; activations 1–4 are legitimate, the fifth fails. Malformed, negative, overflowing, or duplicate entries fail rather than resetting the guard. Call before both exec and supervised launches.

`Enable` requires BinDir, Self, StateDir, and PathEnv; HOME/XDG defaults belong to the caller. It requires a discoverable real gh, creates the missing BinDir (0755), and installs an absolute resolved symlink target. Foreign collisions fail unchanged; existing owned shims are idempotent or atomically upgraded. `OnPath` means the shim directory appears on PATH; empty `FixHint` means this exact shim is first. Fix hints are POSIX export commands, never automatic shell edits.

After Enable, the management CLI must call `ProbeShim(shim, env)`. This executes the shim with the supplied environment plus one GHOK_PROBE=1, expecting exactly `ghok-probe\n`. The wrapper must handle this **before discovery/hooks**. Enable deliberately does not implicitly execute Self. `Disable` removes only owned symlinks in the requested BinDir and clears that path's state record; repetition is a no-op.

`Checks` returns appendable Check records (`Name`, `Status`, `Detail`, `Fix`); statuses are ok/warn/fail/info. DoctorOptions carries Self, Version, PathEnv, Override, BinDir, StateDir, Env, and optional Stdin. Recorded shim paths take precedence over BinDir, which is only the fallback when state is empty or absent. Nil Env inherits the current environment; nonnil Env is exact. Stdin defaults to os.Stdin and is inspected, never consumed.

## Ownership

`<StateDir>/shims.json` maps canonical absolute shim paths to absolute link targets. State writes use temporary files and rename, mode 0600; newly created StateDir uses 0700. Existing directory permissions remain unchanged. BinDir aliases through directory symlinks share state keys.

An owned shim must be a symlink with either a matching recorded target (even if that old binary is gone), a resolved regular target named ghok, or a target SameFile as Self. Requiring recorded-target equality prevents stale state from authorizing deletion of a replacement foreign symlink. Renamed binaries remain manageable through matching state. Enable also excludes recorded old shims during upgrade discovery. A ghok filename is not verified binary identity: this is bookkeeping, not a security boundary.

New links use os.Symlink, which fails on collision; owned upgrades use temporary symlink + rename. Corrupt/unreadable state fails explicitly. Serialize shim install/uninstall: concurrent external path/state mutation is unsupported, and POSIX path operations cannot transact against unrelated writers. Real gh, shell rc files, process PATH, gh configuration, and git configuration are never modified.

## gh v2.102.0 findings

Read upstream sources at tag v2.102.0:

- [`internal/ghcmd/cmd.go`](https://github.com/cli/cli/blob/v2.102.0/internal/ghcmd/cmd.go): executablePath honors inherited GH_PATH first. Otherwise it starts from os.Executable and searches PATH for the same installation, preserving stable Homebrew symlink paths across upgrades.
- [`pkg/cmd/factory/default.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/factory/default.go): passes the selected executable path to the extension manager and git client.
- [`pkg/cmd/extension/manager.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/extension/manager.go): Dispatch appends GH_EXTENSION=1 and GH_PATH=<selected executable path> to extension environments.
- [`pkg/cmd/auth/setupgit/setupgit.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/auth/setupgit/setupgit.go) and [`pkg/cmd/auth/shared/gitcredentials/helper_config.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/auth/shared/gitcredentials/helper_config.go): setup-git writes `!<selected executable path> auth git-credential` into global git configuration.

Normally, real gh launched through the shim selects its own installation's stable path. Extensions calling `$GH_PATH` and configured credential helpers therefore bypass hooks; extensions calling plain `gh` can reenter the PATH shim. **Selection is not unconditionally os.Executable:** inherited shim-valued GH_PATH can propagate recursion and affect credential-helper configuration. Doctor flags this; ghok deliberately does not rewrite GH_PATH. Direct real-gh/API calls cannot be prevented by ghok.

## Diagnostics and limits

Doctor checks binary/version label, real gh, exact shim precedence, ownership, recursion, gh compatibility, and process capabilities. Recorded renamed shims are flagged and never executed for version checking. Probes and `gh --version` execute explicit paths with a ten-second timeout, bounded stdout (64 KiB), and bounded inherited-pipe waiting. Older/unparseable/failed version checks warn; 2.102.0+ is informational. The threshold is the verified normalization/source baseline, not a hard functional minimum or future compatibility guarantee.

Plain `ghok doctor` inspects the installs recorded in `shims.json`, including custom `enable --bin-dir DIR` paths, rather than an absent default shim. Multiple records produce one `path-precedence`/`shim-ownership` pair per shim in sorted path order; each still checks exact PATH precedence and current symlink ownership, so replaced foreign links are not trusted. Without records, doctor checks `~/.local/bin/gh`. Corrupt/unreadable state fails the shim diagnostics instead of silently falling back.

macOS/Linux amd64/arm64 share installation semantics. Native termios ioctl distinguishes PTYs from files and /dev/null (Darwin TIOCGETA, Linux TCGETS). Doctor reports /dev/tty access and foreground-process-group job-control requirements without changing process groups; Linux additionally reports /proc availability. Windows/other OS builds are unsupported.

Discovery cannot identify an arbitrary renamed, different ghok binary without identity/state evidence; GHOK_DEPTH bounds the resulting activations. Executability uses file type and permission bits, not ACL/noexec-mount effective-access checks. Relative overrides are unsupported. No process-control certification is implied by informational capability checks.

## Verification

Hermetic temp-directory tests cover ordered/duplicate installs, self/renamed/other shims, absent gh, broken/looping links, permission/type/PATH rejection, overrides, depth boundaries, Homebrew upgrade plus old-version deletion, unchanged foreign collisions/replacements, repeated lifecycle, directory aliases, probes, doctor custom/multiple installs, invalid state, precedence, recursion/version/failure/timeout, and PTY detection. E2E covers custom enable followed by plain and JSON doctor. No network. BenchmarkFindRealGh measures stat-only discovery.

Installation unit tests and the custom-install doctor E2E passed on macOS arm64, Linux arm64 (`node:22-bookworm-slim`), and translated Linux amd64 (`mcr.microsoft.com/powershell:7.4-ubuntu-22.04`). Required root build/vet/full tests/race and make build passed on macOS arm64. Linux runs used cross-built test/fixture binaries with network disabled, read-only mounts/images, and tmpfs; Linux race was not run. The unrelated Linux arm64 process job-control failure is recorded in [cli.md](cli.md).
