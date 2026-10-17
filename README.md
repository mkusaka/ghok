# ghok

`ghok` wraps the GitHub CLI with pre/post hooks, semantic operation
normalization, local policy checks, and redacted JSONL diagnostics. It is a
CGO-free Go binary for macOS and Linux, amd64 and arm64; the runtime dependency
is an independently installed `gh`. Install the shim machine-wide:
repos without a ghok config are unaffected: gh is exec'd directly.

**This is a convenience guardrail, not a security boundary.** Direct real-`gh`
paths, `curl`, other clients, extensions, and credential helpers can bypass it.
Use GitHub branch protection and rulesets for authoritative enforcement.
Hooks are trusted programs running as your user. Invalid project configuration
cannot disable user hooks or policy. Invalid user configuration fails closed
when raw YAML declares enforce mode; observe/undetermined mode fails open.
Set `GHOK_FAIL_CLOSED=1` to reject any invalid configuration.

## How it works

The executable name selects its role; `ghok pr merge ...` is not wrapper syntax.
For tests, CI and scripts without a shim, use `ghok exec -- <gh args>`;
for an interactive shell without a shim, see [shell function](#shell-function-without-a-shim).

```text
basename(argv[0])
  ghok (or any name except gh) -> management commands
  gh -> discover real gh -> classify the operation -> resolve only needed context
     -> run trusted transforms serially -> re-normalize changed requests
     -> evaluate policy + pre-hooks -> deny: exit 77/78, no gh
     -> execute original request (or the successfully transformed request)
        no matching post-hook/live stdin pump: exec replaces wrapper
        otherwise: supervise gh -> post-hooks -> original exit/signal
```

Alias expansion is analysis-only unless a transform changes the request; changed
requests use expanded argv. Without transforms, real gh receives the original arguments.
Uncaptured output uses inherited descriptors, not a PTY emulation.
Post-hooks never change gh's status. Capture is opt-in and changes gh's
stdout/stderr to pipes; see [compatibility](docs/compatibility.md).

`ghok exec -- pr view 123 -R org/repo` enters that same wrapper pipeline once,
with real-gh `argv[0]` set to `gh`. Discovery excludes ghok itself and rejects
shim-valued `GHOK_REAL_GH` / `runtime.realGh`; recursion depth and nested-token
checks, policy, hooks, logging, status and signals are identical to the shim.
It is **not a policy bypass** and does not require installing a shim on PATH.
Hooks and JSONL logs record `invocation.via: "exec"` or `"shim"`.

### Request transforms

`transforms.pre` runs trusted programs before policy and pre-hooks, in user then
project order. Each program sees the current normalized request and may return
restricted `set`/`unset` edits. This is separate from hooks: hooks still judge
requests rather than rewrite them.

```yaml
transforms:
  pre:
    - id: attribute-session
      match:
        operation: "*comment.create"
        env:
          CLAUDE_CODE_SESSION_ID: "*"
      command: ["./transforms/attribute-session"]
      timeout: 5s
      onError: deny
```

Build the [session attribution example](examples/transforms/attribute-session/main.go)
with `go build -o "$config_dir/transforms/attribute-session" ./examples/transforms/attribute-session`
after creating that private directory. It appends
`\n\nfrom session_id: $CLAUDE_CODE_SESSION_ID` to supplied comment bodies; absent
session IDs produce no output and no change. See [example config](examples/config.yaml).

Project transforms require explicit `ghok config trust`, just like project hooks;
inactive transforms are ignored, never gates. A failed `onError: deny` transform
stops execution with **78 even in observe mode**. With `onError: allow`, ghok rolls
back **all** transforms, skips the remainder, and evaluates/executes the original
request. File/stdin edits use private temporary transport, never overwrite the
original file, and are cleaned up after use. See the
[transform protocol](docs/hook-protocol.md#request-transforms) for editable paths,
unsupported sources and diagnostic behavior.

### Exit status

| Origin | Code | Meaning |
| --- | --- | --- |
| gh | 0 | Success. |
| gh | 1 | Command failure. |
| gh | 2 | Command canceled. |
| gh | 4 | Authentication required. |
| gh | 8 | A command-specific check failed (for example, pending `gh pr checks`). |
| ghok | 77 (`EX_NOPERM`) | Policy/rule denial or a hook **returned** deny. Content denial takes precedence over hook errors. |
| ghok | 78 (`EX_CONFIG`) | Transform failure under `onError: deny` (either mode), hook malfunction under `onError: deny`, an inactive gate, or invalid configuration failed closed. |
| ghok | 1 | ghok internal failure. |
| ghok | 127 | Real gh was not found. |

Other gh statuses and terminating signals pass through unchanged. Observe-mode
would-deny never changes gh's status. Post-hooks cannot change it either.
`ghok hooks test` is a management diagnostic: expectation mismatch, denial or
hook execution failure still exits **1**, not 77/78; usage errors exit 2.

## Install

Install [GitHub CLI](https://cli.github.com/) first. Normalization is based on
**gh 2.102.0**; other versions may add syntax that becomes unknown. Build and
test with the Go version in [go.mod](go.mod).

### Homebrew (macOS / Linux)

```sh
brew install mkusaka/tap/ghok
ghok shim install          # creates ~/.local/bin/gh -> ghok; shell rc files are not modified
ghok doctor
```

The formula installs a prebuilt binary and depends on Homebrew's `gh`. Keep
`~/.local/bin` before `$(brew --prefix)/bin` on PATH. `brew upgrade ghok` keeps
the shim working because it points at the stable `opt/ghok` path, not a Cellar
version directory.

### Linux / WSL

Homebrew on Linux uses the same formula: `brew install mkusaka/tap/ghok`.
Without Homebrew:

```sh
# mise (no plugin or registry needed; installs from GitHub releases)
mise use -g github:mkusaka/ghok

# Debian/Ubuntu (incl. WSL) or Fedora/RHEL packages attached to each release
gh release download --repo mkusaka/ghok --pattern 'ghok_linux_amd64.deb'   # or arm64 / .rpm
sudo apt install ./ghok_linux_amd64.deb                                     # or: sudo dnf install ./ghok_linux_amd64.rpm
```

The `.deb`/`.rpm` files are not an apt/dnf repository, so upgrade by installing
a newer package; Homebrew and mise upgrade in place.

### GitHub Actions

```yaml
- uses: mkusaka/ghok@<commit SHA> # vX.Y.Z
- run: gh pr merge 123 --squash   # now goes through ghok (pre/post hooks, policy)
```

The action installs the ghok release that matches its own ref (a release tag,
or the SHA of one), verifies it against the immutable release attestation and
checksums, and puts a `gh -> ghok` shim first on PATH (`shim: false` installs
only). Inputs: `version` (`vX.Y.Z` or `latest`, overrides the ref), `shim`,
`token`; outputs: `version`, `path`. Configure it like anywhere else, e.g. with a
`config.yaml` under `GHOK_CONFIG_DIR` or a project `.ghok.yaml` (trust its hooks explicitly).
Linux and macOS runners only.

### Go install

```sh
go install github.com/mkusaka/ghok/cmd/ghok@latest
ghok version
```

Put `$(go env GOPATH)/bin` (or your `GOBIN`) on PATH. Without release ldflags,
`ghok version` uses Go's embedded module version (for example `v0.0.3` from
`go install ...@v0.0.3`) and VCS revision when available. Injected release
version/commit values take precedence. If the module version is unknown or
`(devel)`, the version stays `dev`; `ghok doctor` warns that features may differ
from the docs.

### Release archive

Download a tag from [Releases](https://github.com/mkusaka/ghok/releases). Archives
are built by GoReleaser, named `ghok_<darwin|linux>_<amd64|arm64>.tar.gz`, and
contain `ghok`, `README.md`, and `LICENSE` at the top level. On Apple Silicon
choose `darwin_arm64`; on Intel Macs choose `darwin_amd64`.

```sh
# Run in a new download directory, with no existing files.
gh release download "$version" --repo mkusaka/ghok \
  --pattern '*.tar.gz' --pattern checksums.txt
shasum -a 256 -c checksums.txt
archive="ghok_darwin_arm64.tar.gz" # change OS/arch as needed
gh attestation verify "$archive" --repo mkusaka/ghok \
  --signer-workflow mkusaka/ghok/.github/workflows/release.yml
tar -xzf "$archive" ghok
mkdir -p "$HOME/.local/bin"
install -m 755 ghok "$HOME/.local/bin/ghok"
```

Download all four archives for the complete checksum manifest check. Checksums
detect corruption; provenance associates an artifact with this repository's
release workflow. Releases are published as GitHub immutable releases (assets
and tag cannot change after publication); `gh release verify <tag> -R mkusaka/ghok`
checks the release attestation. Neither proves the source safe. The release workflow uses
trimmed build paths, explicit version/commit injection and commit-timestamped
binaries. Binaries are not Apple Developer-signed/notarized.
See [release engineering notes](docs/notes/docs.md).

### Build from source

```sh
git clone https://github.com/mkusaka/ghok.git
cd ghok
make build
./bin/ghok version
# Install bin/ghok to a stable location already on your PATH.
```

`make build` injects `VERSION` (default: git describe) and `COMMIT` (default:
empty). To label a build explicitly: `make build VERSION=v1.2.3 COMMIT=<sha>`.

### Agent skill

An [Agent Skill](skills/ghok/SKILL.md) for using ghok from coding agents
(Claude Code, Codex, Cursor, ...):

```sh
npx skills add mkusaka/ghok
```

## Shim install, uninstall, upgrade

Recommended: install the shim once on your normal machine-wide PATH, not per
repository. With no user config and no applicable project `.ghok.yaml`,
repos without a ghok config are unaffected: gh is exec'd directly.
No logger, normalizer, stdin spool, resolver, or hook-warning state is opened.
Creating a user config intentionally enables ghok in every repository.
Scoped activation through your own PATH management is possible but unsupported.
direnv/mise are optional human helpers only: agents' non-interactive shells do
not load direnv, and their allow lists are path-based like the old trust model,
not content-addressed hook trust.

```sh
ghok shim install                    # owned symlink ~/.local/bin/gh -> ghok
export PATH="$HOME/.local/bin:$PATH"
command -v gh                  # should identify the shim
ghok doctor
ghok shim uninstall                   # removes only the owned shim, idempotently
```

`shim install` requires a discoverable real gh. It refuses foreign collisions,
checks the installed shim with a probe, and prints PATH instructions rather
than editing shell startup files. Shells may cache command paths; refresh your
shell's command cache (`hash -r` in bash, `rehash` in zsh) after PATH changes.
For a different directory use the same `--bin-dir DIR` on enable and disable.
Installation state is stored in the ghok config directory. Plain `ghok doctor`
automatically checks every shim recorded there, including custom locations;
without records it checks `~/.local/bin/gh`.

Upgrade the `ghok` executable at its stable installation path, then run
`ghok shim install` and `ghok doctor` again. If the executable moved, `shim install` replaces
only the owned shim target. Real gh discovery follows current PATH-facing
symlinks, so Homebrew's Cellar path need not be pinned.

To uninstall, disable **before** deleting the executable, then remove your
installed `ghok` binary. Configuration, hooks, logs, and ownership state are
retained; remove them separately only if you no longer want them. ghok does not
change real gh, gh/git configuration, or shell rc files.

### Shell function (without a shim)

To use ghok in an interactive shell without a shim, add this to that shell's
interactive startup file:

```sh
eval "$(ghok shell-init zsh)"    # or bash; fish: ghok shell-init fish | source
```

This defines `function gh { ghok exec -- "$@"; }` (a function, not an alias, so
it also applies inside the same `zsh -c '...'` string that defines it). gh
completion keeps working because completion is keyed by the name `gh`.

By default, this covers only shells that read that interactive startup file.
Non-interactive shells need their own startup setup:

- zsh reads `~/.zshenv` for interactive and non-interactive shells. Put the
  guarded zsh `eval` there to cover `zsh -c`.
- For non-interactive bash, including `bash -c`, bash reads the file named by
  `BASH_ENV`, not `~/.bashrc`. Export `BASH_ENV` from the parent environment
  (for example, in `~/.zshenv`) to a file that runs the guarded bash `eval`.
  This defines a bash function; bash does not inherit the zsh function.

For example:

```sh
# ~/.zshenv
if command -v ghok >/dev/null 2>&1; then
  eval "$(ghok shell-init zsh)"
fi
export BASH_ENV="$HOME/.config/ghok/bash_env"

# ~/.config/ghok/bash_env
if command -v ghok >/dev/null 2>&1; then
  eval "$(ghok shell-init bash)"
fi
```

For interactive bash, put the guarded bash `eval` in `~/.bashrc`. This `BASH_ENV`
setup covers bash processes started from that zsh; bash started without that
environment variable remains uncovered.

These shell functions do not cover `/bin/sh` or direct `gh` process launches
that do not start a configured shell. They also do not cover `xargs gh`,
`env gh`, `sudo gh`, `nohup gh`, `exec gh`, or `command gh`. Use the PATH shim
for broader coverage across scripts and agent shells. Both may be installed:
`ghok exec` skips the shim when finding real gh, so gh still runs once.
`ghok doctor` notices recent `ghok exec` use and then reports a missing shim as
info, with this coverage gap, instead of a warning.

## Configuration and hooks

Create a commented starter file (with a yaml-language-server schema reference):

```sh
ghok config init                 # <repository root>/.ghok.yaml (default scope: project)
ghok config init --scope local   # <repository root>/.ghok.local.yaml (personal)
ghok config init --scope user    # the global config.yaml
```

None overwrites an existing file. The project file lives at the repository
root, so it can be committed and shared. Its declarative policy applies
automatically, but cloning a repository never activates its hooks.

`.ghok.local.yaml` is a personal config for one checkout, like `CLAUDE.local.md`.
Layers load in the order user config → `.ghok.yaml` → `.ghok.local.yaml`; local
hooks and transforms run after the project's. Because you write it yourself, its
hooks and transforms run **without** `ghok config trust`. Otherwise it follows the
project rules: policy can only tighten, and `runtime`, `logging`, `trust` and
hook `files` are rejected. `config init --scope local` adds `/.ghok.local.yaml`
to the repository's `.git/info/exclude` (never `.gitignore`). ghok ignores the
file while git tracks it, because a committed copy would let any clone run hooks
without trust. It works with or without a `.ghok.yaml`, and is per checkout, so
each worktree has its own.

The config format has a JSON Schema generated from ghok's Go config types:
[`schema/config.schema.json`](schema/config.schema.json) for `config.yaml` and
[`schema/project.schema.json`](schema/project.schema.json) for a project
`.ghok.yaml`. Print them with `ghok schema config` or `ghok schema project-config`;
regenerate all schemas with `go generate ./internal/schema` (also `make schema`).
Curate field tags in the Go types and starter templates, not generated JSON.
For editor completion, start the YAML file with:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/mkusaka/ghok/main/schema/config.schema.json
```

Global config: `GHOK_CONFIG_DIR/config.yaml`, otherwise
`$XDG_CONFIG_HOME/ghok/config.yaml`, otherwise `~/.config/ghok/config.yaml`.
With either config present, defaults are **observe**, unknown-mutation **audit**,
and logging **enabled**. With neither present, the wrapper execs gh directly.
Project policy can only tighten explicitly set mode/unknown handling and append
rules/protected repositories; it never weakens global policy.
Projects cannot set runtime or logging. `trust.include` is project-only; user
configuration has no `trust.projects` setting.

Project discovery stops before `$HOME`, so a dotfiles repository at `$HOME`
does not supply project config. Project read/parse/validation failures discard
only the project overlay and appear in validate/doctor/explain notes and log
diagnostics. Project unknown keys (including nested keys) are ignored with notes;
user config remains strict. An invalid user config declaring
`policy.mode: enforce` in raw YAML prints an error every time, exits 78, and never
starts gh. Observe or undeterminable mode runs gh without hooks/policy with a
warning throttled to once per ten minutes per error. `GHOK_FAIL_CLOSED=1` also
rejects invalid project files and unreadable/unparseable user config. A missing
user config uses defaults when a project config applies; with neither file,
gh is exec'd directly. Validate and doctor explain the selected failure behavior.

Inspect project hooks, then manage their activation:

```sh
ghok config trust                   # current repository-root .ghok.yaml
ghok config trust --hook check --hook audit # trust selected hooks; retain siblings' trust
ghok config trust /path/to/repo/.ghok.yaml
ghok config trust --dry-run          # review changes and proposed trust; writes nothing
ghok config trust --no-diff          # keep file/mode summaries, omit raw content diffs
ghok config trust --list             # project records, per-hook digests and metadata
ghok config untrust                 # remove this project's hook trust; policy remains
ghok config untrust --all            # remove every project trust record
```

Trust is **per hook** and follows content: a hook is active when its exact digest
was trusted anywhere on this machine, so identical worktrees and clones share
trust. All worktrees of a repository (same Git common directory) share one record;
earlier grants stay valid, so worktrees on older branches keep working.
Each digest covers the canonical, defaulted hook definition, its repository-local
executable, its `files`, and project `trust.include` files. YAML comments,
formatting, policy changes and unrelated hook edits/reordering do not revoke trust.
List position is not hashed: express ordering guarantees with `dependsOn`.
A definition or dependency change disables only affected hooks; shared files
affect every hook that uses them, and `trust.include` applies to **every hook**:

```yaml
hooks:
  pre:
    - id: check
      command: ["./hooks/check"]
      files: [".github/scripts/check-pr-description.ts"]
trust:
  include: [".gitleaks.toml"]
```

`files` is project-only; user hooks run without trust and cannot declare it.
Paths must be repository-relative, without `..` or globs. Directories are rejected;
list individual files. Symlink targets must remain inside the repository.

Project hook commands must start with `./` or be absolute; user hooks may still
use bare PATH names. Relative paths resolve under the repository root. Manifest
files must resolve within that root (including symlink targets) and be regular
files, with at most 256 files and total bytes bounded by the effective
`runtime.fileMaxBytes`. Missing files are represented by markers, so creating
one changes the digest. Absolute external executables are not hashed.
Covered files are deduplicated and list all affected hook IDs. A covered byte,
executable-bit or missing-file change revokes only its users' trust. Policy-only
configs skip manifest computation.

A **gate** is a pre-hook with effective `onError: deny` (the pre default).
In enforce mode, matching invocations fail closed with `config <id>` / exit 78
if a gate changed since trust, or is untrusted while another project hook is
trusted and enabled. Observe records would-deny without blocking. A never-trusted
project still ignores hooks with a warning, without this denial.

Interpreters, PATH tools and unlisted imports/dependencies/data are not covered.
Add hook-specific dependencies to `files`, or common dependencies to `trust.include`.
Trust review groups definitions and changed files by hook, with shared files once.

`<config-dir>/trust.json` stores per-hook digests and canonical definitions with
`firstPath`, `configPath`, `trustedAt`, hook IDs, per-file executable bits and
hashes/missing markers, plus `commit` (HEAD SHA), resolved `commonDir`, and normalized
credential-free origin `remoteUrl`. Git metadata is read directly, never by running git.
`ghok config trust --list` shows project records and per-hook digests.
Old manifest records grant no trust: run `ghok config trust` once after upgrading.
Provenance is diagnostic, not a digest input. The store is written atomically
with mode 0600 in a 0700 config directory.
Malformed entries are skipped individually with notes naming their keys; valid
entries remain trusted. An unreadable or invalid top-level store grants no trust.
Declarative policy still applies.

Before re-trusting, use `ghok config trust --dry-run [PATH]`: it validates and
prints the comparison, proposed digest/files and enabled commands without writing
trust, blob or cache state. `config trust`, and `config validate`/`doctor` when
hooks are ignored, show changed/added/removed manifest files, executable-bit
changes (`mode 0644 → 0755`) and the previous record's timestamp/path. Modes
describe the manifest's executable bit, not every filesystem permission bit.
Review and `changed since trust` use this repository's record first; otherwise they select a record by resolved Git
common directory, same `firstPath`, same normalized origin, then at least two
distinct shared file-content SHA256s (paths may differ). Sharing only
`.gitleaks.toml` does not select an unrelated repository. Origins normalize
scp-like/SSH/HTTPS spelling, lowercase hosts, remove userinfo, ports 22/443 and
trailing `/`/`.git`; repository path case is retained and `insteadOf` is ignored.
No match is reported as `no previous trust record for this repository`.
Trust itself comes only from matching digests.

Only `config trust` and `config trust --dry-run` show unified diffs against
**the exact bytes previously trusted**, including uncommitted files, not HEAD.
The displayed diff masks credentials (tokens, keys) like `explain` does; pass
`--show-secrets` to see it raw, or `--no-diff` to omit content output. Stored copies
are **unredacted** (byte-exact) and may contain secrets. Copies live at `<config-dir>/trust/blobs/<sha256>`
(0700 directories, 0600 files), deduplicated with 1 MiB per-file and 64 MiB total
limits; larger files remain hash-only, and full stores skip new copies with a
note. Missing/corrupt copies explain why no diff is shown; hashes are verified
on read. Binary changes (NUL within the first 8 KiB) show short hashes instead
of contents; diffs over 20,000 lines per side are omitted. Text splits on LF
only and displays CR as `^M`. HEAD is provenance shown by `trust --list`; no
Git object lookup, executable or fetch is needed for diffs. Trust/untrust
collect copies no longer referenced by any retained digest; `untrust --all`
purges all stored copies. See [security](docs/security.md#project-configuration-trust).

Trust and untrust reject missing configs and paths other than a repository-root
`.ghok.yaml`. Untrust is idempotent and removes the whole project's authorization;
`--all` clears every project record. Untrust removes the repository record from any
of its worktrees; an identical hook trusted for another repository stays trusted.
This is not a security boundary against concurrent replacement or malicious users.
Validate, doctor, explain and hooks list report per-hook reasons, explicitly naming
inactive gates. Ignored-hook warnings are throttled for ten minutes per canonical
project path and current ignored-hook IDs/digests/reasons. Changed-file lists show
at most three names. Disable only this
warning with user config `logging.ignoredHooksWarning: false` (default true);
denied, nested, invalid-config, and policy-only invocations do not emit it.

```yaml
version: 1
policy:
  mode: enforce
  unknownMutation: audit
  protectedRepositories: ["org/repo"]
  protectedUnknown: deny
hooks:
  pre:                     # run before gh; may deny
    - id: deny-admin-merge
      match:
        operation: "pr.merge.*"
      command: ["./hooks/deny-admin-merge"]
      timeout: 5s
      onError: deny
  post:                    # run after gh; never change its result
    - id: audit-merge
      match:
        operation: "pr.merge.*"
      command: ["./hooks/audit-merge"]
      timeout: 5s
      onError: ignore
```

A hook's phase is the `hooks.pre` / `hooks.post` list that holds it; hooks have
no `phase:` key. Hook IDs are unique across both lists.

Use the compiled Go examples in [examples/hooks](examples/hooks) and follow
[examples/config.yaml](examples/config.yaml) to install them. The pre-hook
denies explicit `admin: true` merge requests and otherwise abstains; it does
not validate every GitHub operation. The audit hook records command outcomes,
not confirmed server-side merge events. A Python equivalent is optional;
Python is not a ghok runtime requirement.

Each list runs in order, user config hooks first, then project hooks.
Concurrency follows GitHub Actions'
[parallel steps](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax):

```yaml
hooks:
  pre:
    - id: lint-title              # runs first
      command: ["./hooks/lint-title"]
    - parallel:                   # members overlap; the next item waits for all
        - id: check-ci
          command: ["./hooks/check-ci"]
        - id: check-reviews
          command: ["./hooks/check-reviews"]
    - id: scan-secrets            # starts, and later hooks do not wait for it
      background: true
      command: ["./hooks/scan-secrets"]
    - id: require-green
      dependsOn: [check-ci]       # skipped unless check-ci succeeded
      command: ["./hooks/require-green"]
    - wait: scan-secrets          # or [id, ...]; `- wait-all:` joins every background hook
```

- `background: true` starts a hook without waiting for it; `- wait: id` or
  `- wait-all:` items join background hooks. `parallel` is shorthand for
  background members joined at the end of the block. A phase always waits for
  every hook before deciding, and project hooks start after all user hooks.
- `runtime.hookConcurrency` (default 4; Actions allows 10) caps how many hooks
  run at once; the rest queue.
- Unlike Actions, an effective pre-hook deny cancels every running hook at once,
  because the denial is final. Other failures follow each hook's `onError` and
  never stop later hooks. `dependsOn` (earlier hooks only) skips a hook unless
  its dependencies succeeded and passes their results in `previous`; use it, not
  list position, when a hook must run only after a gate allowed.
- `cancel` is not supported: hooks are short-lived and have a `timeout`.
- `ghok config validate` rejects empty or nested `parallel` blocks, extra keys
  beside `parallel`/`wait`/`wait-all`, and waits for IDs that are not earlier
  background hooks of the same list. `ghok hooks list` shows each hook's
  `background` flag and the
  hooks it waits for.

A hook reads one schema-version-1 JSON document from stdin and writes one
response to stdout, for example:

```json
{"schemaVersion":1,"decision":"deny","reason":"admin merge is not permitted"}
```

Exit zero with empty stdout means abstain; nonzero exit, timeout, or invalid
response is an **outcome**, handled by `onError`. Commands are argv arrays,
not shell snippets. Relative executable paths containing `/` are relative to
the config directory; bare names use the hook's PATH. Hooks inherit the real
environment (including credentials) and receive **raw input by default**, including
original/expanded argv, normalized texts and parameters. Secret scanners see the
same credentials gh will submit. Add `redactInput: true` to any hook that forwards
input elsewhere (notifiers, webhooks); it receives redacted copies with a top-level
`redactions` total and a `redactions` count on every Text. Counts measure substitutions,
not unique secrets; zero does not prove input contains no secrets. Deny on `truncated`
or unavailable input when complete inspection matters, and on `redactions > 0` when
masked content cannot be safely evaluated. Logs and displayed diagnostics remain
redacted; JSONL records include their own `redactions` count.
See the complete [hook protocol](docs/hook-protocol.md).

Machine-readable contracts: [`schema/hook-input.schema.json`](schema/hook-input.schema.json)
and [`schema/hook-response.schema.json`](schema/hook-response.schema.json), also
printed by `ghok schema hook-input` and `ghok schema hook-response`. Generate
TypeScript types for a hook with:

```sh
npx json-schema-to-typescript schema/hook-input.schema.json > hook-input.d.ts
npx json-schema-to-typescript schema/hook-response.schema.json > hook-response.d.ts
```

Empty typed hook arrays are omitted, not `null`; treat absent arrays as empty.
`schemaVersion` is fixed at 1. Additive fields do not bump it; renames/removals do.

### Denial output contract

In enforce mode, stderr starts with this stable machine-readable line:

```text
ghok: denied by <source>[ (error: <kind>)][ (+N more)]
```

`source` is `transform <id>`, `hook <id>`, `rule <id>`, `policy unknownMutation`,
`policy protectedUnknown`, `policy protectedRepositories`, `policy unknownInput`,
`config <id>` for an inactive gate, or `config` for invalid configuration.
Hook error kinds are `timeout`, `exit <code>`, `invalid response`, `spawn failed`,
or `canceled`. Transform failures happen before policy/hooks and use their outcome
kind: `error`, `timeout`, `canceled`, `invalid-response`, or `unsupported-edit`.
`N` counts remaining denial entries.

Order is deterministic: denying rules in config order, policy handling, inactive gates,
hooks that returned deny in selection order, then hook-error denials in
selection order. Content denial therefore wins over an earlier hook error.
Each entry follows as `  - <source>: <message>`, using the reason when there
is no message. Only the message is displayed when both exist (equal values
appear once); further message lines use four-space indentation. Reasons and
messages are never joined with `"; "`. All terminal values remain
credential-redacted.

Hook-authored terminal text uses one sanitizer in denial output, `hooks test`
text, explain, and hook/config notes in doctor/validate and hook listings/trust
review. Newlines and tabs survive; other C0/C1 controls (including ESC, BEL, CR
and DEL) and invalid UTF-8 bytes become visible `\xNN` escapes. Each denial entry
is limited to 40 lines, 4 KiB and 500 Unicode characters per line, including
formatting and truncation markers. Long lines end in `…`; omitted lines end
with `… (N more lines truncated)` (the marker occupies one of the 40 lines).
The same bounds apply per text diagnostic, not to the whole report.
`--show-secrets` does not disable terminal sanitization.
JSON (`--json`, `--print-input`) and JSONL logs retain their full structured
values and existing redaction; terminal escaping/truncation does not alter them.

```text
ghok: denied by hook check (+1 more)
  - hook check: Review required.
    Ask the repository maintainer.
  - hook scanner: hook timed out
```

This exits 77 because a hook returned deny. A timeout-only denial instead
starts `ghok: denied by hook scanner (error: timeout)` and exits 78.

### Policy and unknown handling

| Setting | Behavior |
| --- | --- |
| `mode: observe` (default) | Run hooks and record would-deny; never block gh on policy/hook denial. |
| `mode: enforce` | Any policy or effective pre-hook deny prevents execution. Allow cannot override deny. |
| `unknownMutation: allow/audit/deny` | Readable requests whose potentially writing operation could not be classified; default audit. |
| `unknownInput: allow/audit/deny` | Requests that could not be read (`input-unavailable`, `input-too-large`, including stdin timeouts); default audit, independent of unknownMutation. |
| `protectedUnknown: allow/audit/deny` | Unknown writes/unresolved potentially writing targets with a protected list; default deny. |

Known writes with resolved protected targets are **not automatically denied**:
add matching rules/hooks for your desired policy. Unresolved repository/host
matches apply conservatively by default. Set a rule's `onUnresolved: ignore`
to skip that rule when its target criteria cannot be evaluated; the default
`deny` retains conservative matching. Other rules and protected-target handling
still apply. Explain and `hooks test --json` identify deciding maybe-matches as
`matched conservatively (target unresolved)`. Every considered operation in a
multi-mutation document participates; proven skipped/unselected fields do not.
Operation globs match **action**, not family: `pr.merge.*` covers descendants,
while `pr.merge` is an exact action. Match criteria are AND, list entries OR.

`runtime.resolveAPI: auto` (default) permits bounded read-only API lookups when
repository/host criteria could affect a rule or enabled pre/post hook, in either
mode, or protected-repository handling in enforce mode. Logging alone never
triggers a lookup. `never` disables implicit wrapper API resolution; explicit
management `--allow-api` remains available. Lookup failure retains conservative
maybe-matching and adds a log diagnostic.
Resolver requests default to a 2s timeout. Successful node identities are cached
for 24h in `<config-dir>/state/nodes.json` (0600, at most 5,000 entries); corrupt
or expired entries are ignored. Node lookups populate kind/repository/number and
specialize generic comment/lock actions when the subject type is known.

Actions specialize only as far as resolution permits. GraphQL `addComment` and
the shared REST issue-comment endpoint can retain `comment.create` with
`target.kind: unknown`; opaque node IDs do not establish PR versus issue.
To avoid missing comment writes, match the generic action **and** known-subject
names (the same match block works on a pre-hook):

```yaml
policy:
  rules:
    - id: check-comment-writes
      match:
        operation: ["comment.*", "pr.comment.*", "issue.comment.*"]
        effect: write
      decision: deny
```

Native `pr comment` / `issue comment` now use `pr.comment.create` /
`issue.comment.create` (or `.edit` / `.delete` for last-comment flags), not the
old exact `pr.comment` / `issue.comment` names. See the
[cross-layer taxonomy and safe matching](docs/normalization.md#matching-safely).
`config validate` and `doctor` warn for each operation pattern matching no known
action, including obsolete exact names; warnings do not invalidate the config.


`match.env` applies to rules and pre/post hooks using the caller's invocation
environment, before hook-specific `env` overrides. Names must match
`[A-Za-z_][A-Za-z0-9_]*`; values are case-sensitive `path.Match` globs. `"*"`
means set and non-empty (including values containing `/`); `""` means unset or
empty. Every map entry must match, ANDed with the other criteria. Environment
variables are caller-controlled: this is a guardrail against mistakes, **not a
security boundary**.

```yaml
policy:
  mode: enforce
  rules:
    - id: no-worker-merges
      match: {env: {MALL_GH_WORKER: "1"}, operation: "pr.merge.*"}
      decision: deny
```

`match.token: app|user|unknown|none` matches the kind of credential gh will use
(from the `GH_TOKEN`/`GITHUB_TOKEN` prefix; the token is never logged). Wrappers
such as gain run `gh` through PATH with a GitHub App token (`ghs_`), so they pass
through ghok and can be restricted, e.g. no PR/issue writes authored by the App:

```yaml
policy:
  mode: enforce
  rules:
    - id: no-app-pr-writes
      match: {token: app, operation: ["pr.*", "issue.*"], effect: write}
      decision: deny
      reason: PR/issue writes must be authored by you, not the GitHub App
```

## Operation normalization

ghok classifies every invocation into structured operations at one of three
levels. "L1/L2/L3" are ghok's own labels (they appear as `classification.level`
in the hook JSON and as `match.level` in config), not an established term:

| Level | Input | Method |
| --- | --- | --- |
| L1 | Native commands, such as `gh pr merge 123` | Versioned command/flag registry; records user intent. |
| L2 | `gh api` REST | HTTP method plus typed route; reconstructs fields/body. |
| L3 | `gh api graphql` | GraphQL AST, operation selection, variables, fragments, directives. |

Canonical merge comparison:

| Invocation | Family | Action | Meaning |
| --- | --- | --- | --- |
| `gh pr merge 123 --squash` | `pr.merge` | `pr.merge.request` | User requests merge; server state may select direct/auto/queue behavior. |
| `gh api -X PUT repos/org/repo/pulls/123/merge -f merge_method=squash` | `pr.merge` | `pr.merge.direct` | Direct REST merge request. |
| GraphQL `mergePullRequest` | `pr.merge` | `pr.merge.direct` | Direct mutation request. |
| GraphQL `enablePullRequestAutoMerge` | `pr.merge` | `pr.merge.auto.enable` | Enable auto-merge, not merge completion. |
| GraphQL `disablePullRequestAutoMerge` | `pr.merge` | `pr.merge.auto.disable` | Disable auto-merge. |
| GraphQL `enqueuePullRequest` / `dequeuePullRequest` | `pr.merge` | `pr.merge.queue.enqueue` / `.dequeue` | Queue change, not immediate merge. |

Normalization classifies syntax, not server success. Identity confidence,
target resolution, read/write effect, and execution evidence are separate.
Unknown commands/routes/mutations, shell aliases, extensions, malformed or
inaccessible input remain explicit uncertainty, never a proof of safety.
Unknown flags on a known native command can retain its family/effect with
heuristic confidence; do not treat a retained read effect alone as safe.
Plain aliases are expanded for inspection. API stdin is captured only for a
potentially enforcing policy or potentially matching enabled pre-hook, then
byte-exactly replayed; interactive stdin is never pre-read. Default observe
logging and post-hooks alone leave stdin untouched. Logs record `allow` with
`stdin not analyzed (nothing could act on it)` while retaining the unknown
operation, rather than delaying gh until EOF. `ghok normalize`/`explain` may
still read non-TTY stdin. Registry tables, input rules, and limitations:
[normalization](docs/normalization.md).

## Management commands

```sh
ghok version
ghok doctor --json
ghok config path
ghok config validate
ghok config validate --file /path/to/config.yaml
ghok hooks list --json
ghok hooks test -- pr merge 123 --admin
ghok hooks test --phase post --exit-code 1 --json -- pr merge 123 -R org/repo
ghok hooks test --print-input -- pr create -t Title -b Body -R org/repo
ghok hooks test --print-input --phase post --exit-code 8 --hook audit -- pr checks 123
ghok exec -- pr view 123 -R org/repo
ghok shell-init zsh
ghok normalize -- pr merge 123 --squash
ghok explain -- pr merge 123 --admin
ghok explain --no-resolve -- pr merge 123
ghok normalize --allow-api -- pr merge 123
```

Normalize/explain never execute the requested gh command; explain never runs
hooks and exits zero even when it displays a deny. Normalize, explain and
`hooks test` resolve local git metadata plus `GH_REPO`/`GH_HOST` by default,
without network access. `--no-resolve` disables that lookup; explicitly supplied
targets and normalizer environment inputs remain available. `--allow-api` permits
read-only API queries without requiring `--resolve`; it cannot be combined with
`--no-resolve`. The deprecated `--resolve` flag is accepted as a no-op.
`hooks test` **executes trusted hooks**, using a synthetic result for the post
phase (exit 0, or `--exit-code N` to exercise `match.result`/`match.exitCode`),
but never runs the original command. It reports three separate decisions:

- **Hooks** (`aggregate.decision` in JSON): `deny`, `allow` if some hook allowed,
  or `abstain` if no hook matched/ran or all abstained. Deny dominates allow.
- **Policy** (`policy.decision`): `allow|audit|deny`, after configured-mode handling;
  an observe policy denial is `audit` with `policy.wouldDeny: true`.
- **FINAL** (`final.decision`): combines policy and pre-hook denial. Enforce denies
  if either denies; observe would-deny is `audit` with `final.wouldDeny: true`.

`--expect allow|deny|abstain|audit` keeps comparing **FINAL**, not the hook aggregate:
with policy allow, abstaining hooks leave FINAL `allow`; `--expect abstain` therefore
does not match that case. Use `--expect-hooks allow|deny|abstain` to check the hook
aggregate and `--expect-policy allow|audit|deny` to check policy alone. For example:

```sh
ghok hooks test --expect allow --expect-hooks abstain --expect-policy allow -- version
```

With any expectations, exit 0 only if **all** match, even on denial/hook error;
otherwise exit 1 with each failed expectation named on stderr. Without expectations,
FINAL deny or any hook execution error (nonzero exit/spawn failure, timeout, invalid
response, cancellation) exits 1, otherwise 0. Configuration/analysis failures exit 1;
usage errors exit 2. Other management commands except `exec` use 0 success,
1 failure/denial, 2 usage error. `explain --show-secrets` deliberately disables display redaction;
do not paste that output into logs or tickets.

`hooks test --print-input` prints a single hook-input JSON document without
running hooks or gh. `--phase post` includes a synthetic `result` honoring
`--exit-code`. `--hook ID` selects a matching hook and applies its `redactInput`;
without it, the envelope has an empty `hookId`. Dependency results are absent
because no hooks run. The preview is credential-masked by default, using the
same redactor as explain. If masking replaces values, stderr reports:

```text
N values masked; use --show-secrets for the bytes the hook receives
```

`--show-secrets` shows the actual payload, but **does not override**
`redactInput: true`. Display-only masking does not change the payload's
`redactions` counters. Do not expose raw previews in logs or tickets.
`--allow-api` is incompatible with `--print-input`: preview never starts gh.

Explain starts with the policy decision, deny reasons and pre/post hook
selection, then shows operations and unresolved fields once, followed by rule
matches, additional policy reasons and config notes. Hook outcomes remain unknown;
an enforce denial prevents gh and post hooks even if their matches are listed.
Normalize keeps its JSON contract. `config validate` and doctor's `config` check
report the global source (exists or not found/defaults) and merged project policy.
`hooks list` and explain show ignored project hooks with `untrusted` or
`changed since trust` reasons and guidance to run `ghok config trust`.
`changed since trust` (with the changed files and when it was trusted) is reported
when the same repository trusted another version of that hook ID; otherwise the
hook is simply `untrusted`. Every worktree of a repository gets the same answer.
Hook listings include each hook's source config path in text and JSON;
source is not a YAML setting.

## Performance

Without either config, gh is exec'd directly after config-presence,
real-gh discovery and recursion-depth checks: no logger, normalization, stdin
spool, resolver, or ignored-hooks check. With config, no matching post-hook and
no live stdin pump means exec replaces ghok after checks, without a lingering
supervisor. Observe mode with no hooks/rules and `logging.enabled: false` also
skips normalization, stdin capture, and resolution.
Manifest content hashes are cached under `<config-dir>/state/`, keyed by canonical
path and file metadata; changed metadata forces rehashing. YAML is always read
fresh. See [benchmarks](docs/notes/benchmarks.md) for before/after measurements.

Earlier warm-cache Go benchmarks on Apple M4 Max, Go 1.26.5, macOS arm64:

| Measurement | Median |
| --- | ---: |
| Direct fake gh startup | 7.618 ms |
| Default-logging shim startup | 14.106 ms |
| L1 / L2 / L3 merge normalization | 2.834 / 6.992 / 6.413 µs |
| One trivial pre-hook | 8.021 ms |

The Go benchmark difference of medians was **+6.488 ms**. A separate lead
measurement using a Python subprocess loop, 60 direct and 60 default-config
shim starts, reported **about +3.5 ms** median added latency. Different sampling
methods and uncontrolled host load make these local observations, not a paired
overhead guarantee or portable promise. The disabled-logging shortcut was not
measured; startup allocations describe the parent, not the child. Methodology:
[benchmarks](docs/notes/benchmarks.md).

## Troubleshooting and bypass

- Run `ghok doctor`; inspect real-gh selection, PATH precedence, config,
  executable permissions, recursion warnings, and platform checks.
- `command -v gh` tells you which command your shell selects; it does **not**
  unwrap an enabled shim. Use doctor to find real gh, or invoke its known
  absolute path (for example `/opt/homebrew/bin/gh`). An alias/function can
  take precedence over PATH; `command gh` skips functions, not the PATH shim.
- `GHOK_DEBUG=1 gh ...` exposes redacted logging/wait diagnostics on stderr;
  normal logging errors do not change execution.
- Logs omit known prose (bodies, titles, comments, messages) and summarize other
  whitespace-containing or over-64-byte values. GraphQL documents become
  byte-count/SHA256 fingerprints; repository/action/target metadata stays
  readable. Hook input retains ordinary content; see [logging rules](docs/security.md#files-permissions-and-logging).
- Invalid project config is ignored without disabling user enforcement.
  Invalid user config fails closed for raw enforce mode or
  `GHOK_FAIL_CLOSED=1`; otherwise gh runs without hooks/policy. Invalid-user
  failures use the default log path. Only fail-open warnings are throttled;
  private `<config-dir>/state/` I/O failures leave warnings visible.
- Real gh missing: install GitHub CLI or set `runtime.realGh` / `GHOK_REAL_GH`.
  The wrapper exits 127 with a short diagnostic; doctor retains PATH details.
- `GHOK_REAL_GH=/absolute/path/to/gh` overrides `runtime.realGh`; invalid
  overrides fail rather than falling back. Avoid a shim path and shim-valued
  `GH_PATH`; ghok preserves `GH_PATH` and doctor warns about recursion risks.
- `GHOK_NESTED=1` alone does **not** bypass hooks. Hooks and resolver helpers get
  it together with a random `GHOK_NESTED_TOKEN`, verified against a private
  invocation file under `<config-dir>/state/nested/` and removed after helpers
  finish. For a deliberate one-command bypass, use the real gh's absolute path.
  Discovery/depth checks and optional passthrough logging still apply to valid
  nested invocations. This remains a guardrail, not a security boundary.
- Unknown new syntax: inspect `ghok normalize`/`explain` and its unresolved
  reasons. Do not treat a nonempty candidate repository as confirmed.
- Denial returns 1 before gh starts. Real-gh spawn failures return 126/127;
  executed commands keep their exit status/signal. Foreground wrapper-directed
  SIGINT/SIGQUIT, inherited ignored signals, output capture, and core-dump flags
  have [documented transparency limits](docs/compatibility.md).
- INT/TERM/HUP during resolver calls or pre-hooks cancel/reap helper groups,
  then terminate ghok with that signal without starting the user command.
  During post-hooks they cancel/reap hooks but preserve gh's completed status,
  recording the interruption in logs. Signals ignored at startup remain
  unwatched. SIGKILL cannot be handled; hooks may outlive the wrapper.
- Default logs: `<config-dir>/logs/ghok.jsonl`, rotating above 10 MiB with three
  backups. Logs exclude captured gh output; redaction is not a guarantee for
  arbitrary encodings or unrecognized secrets. See [security](docs/security.md).

## Development and testing

```sh
gofmt -l .                     # must print nothing
go build ./...
make lint                      # golangci-lint v2.14.0 (includes govet)
make test
make race
make fuzz                      # existing target: FuzzParse, 30s
make bench
```

Other fuzz targets (run separately, at least 30s for a full local check):

```sh
go test -run '^$' -fuzz '^FuzzNormalize$' -fuzztime 30s ./internal/normalize/native
go test -run '^$' -fuzz '^FuzzNormalizeREST$' -fuzztime 30s ./internal/normalize/rest
go test -run '^$' -fuzz '^FuzzNormalizeGraphQL$' -fuzztime 30s ./internal/normalize/graphql
go test -run '^$' -fuzz '^FuzzRedactString$' -fuzztime 30s ./internal/redact
```

By default E2E builds ghok/fakegh with the local Go toolchain, dependencies
already cached, and module downloads/toolchain switching disabled. Offline
test-binary runs can instead set both `GHOK_E2E_GHOK_BINARY` and
`GHOK_E2E_FAKEGH_BINARY` to prebuilt executable paths; build ghok with
`-ldflags='-X main.version=e2e-version -X main.commit=e2e-commit'` using the same
Go version as the suite. Tests use temporary HOME/XDG/PATH/configs, fake gh, pipes
and PTYs; no real GitHub writes or auth are needed. POSIX shell fixtures require
`/bin/sh`; bash job-control tests skip if
bash is unavailable. Runtime tests require supported Unix process/PTY
facilities, not only cross-compilation. CI runs native tests, race checks, five
10-second fuzz smokes, and all four cross-builds on each platform.

## Releasing

Record user-visible changes in [CHANGELOG.md](CHANGELOG.md) under `[Unreleased]`
(changes that require users to update config, hooks or scripts go under
**Breaking**). Then, from an up-to-date, clean `main`:

```sh
scripts/release.sh patch        # or minor | major | vX.Y.Z
```

The script turns `[Unreleased]` into the new version section, commits, tags and
pushes. The release workflow first runs the whole CI workflow (every OS/arch,
race, fuzz smoke, lint) on the tag and stops if it fails; it then builds with GoReleaser, attests, publishes an
immutable release with that CHANGELOG section above the generated notes, and
updates the Homebrew tap. It refuses to release a tag without a CHANGELOG section.

## Technical references

- [Architecture](docs/architecture.md)
- [Hook protocol and execution](docs/hook-protocol.md)
- [Normalization model and registries](docs/normalization.md)
- [Security and trust](docs/security.md)
- [Compatibility and transparency](docs/compatibility.md)
- [Detailed engineering records](docs/notes/)

MIT licensed; see [LICENSE](LICENSE).