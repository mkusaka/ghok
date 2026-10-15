# ghok

`ghok` wraps the GitHub CLI with pre/post hooks, semantic operation
normalization, local policy checks, and redacted JSONL diagnostics. It is a
CGO-free Go binary for macOS and Linux, amd64 and arm64; the runtime dependency
is an independently installed `gh`.

**This is a convenience guardrail, not a security boundary.** Direct real-`gh`
paths, `curl`, other clients, extensions, and credential helpers can bypass it.
Use GitHub branch protection and rulesets for authoritative enforcement.
Hooks are trusted programs running as your user. Invalid project configuration
cannot disable user hooks or policy. Invalid user configuration fails closed
when raw YAML declares enforce mode; observe/undetermined mode fails open.
Set `GHOK_FAIL_CLOSED=1` to reject any invalid configuration.

## How it works

The executable name selects its role; `ghok pr merge ...` is not wrapper syntax.

```text
basename(argv[0])
  ghok (or any name except gh) -> management commands
  gh -> discover real gh -> classify the operation -> resolve only needed context
     -> evaluate policy + pre-hooks -> deny (enforce mode): exit 1, no gh
     -> execute original gh argv, cwd, environment, and streams
        no matching post-hook/live stdin pump: exec replaces wrapper
        otherwise: supervise gh -> post-hooks -> original exit/signal
```

Alias expansion is analysis-only: real gh receives the original arguments.
Uncaptured output uses inherited descriptors, not a PTY emulation.
Post-hooks never change gh's status. Capture is opt-in and changes gh's
stdout/stderr to pipes; see [compatibility](docs/compatibility.md).

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

## Configuration and hooks

Create a commented starter file (with a yaml-language-server schema reference):

```sh
ghok config init                 # <repository root>/.ghok.yaml (default scope: project)
ghok config init --scope user    # the global config.yaml
```

Neither overwrites an existing file. The project file lives at the repository
root, so it can be committed and shared. Its declarative policy applies
automatically, but cloning a repository never activates its hooks.

The config format has a JSON Schema generated from ghok's Go config types:
[`schema/config.schema.json`](schema/config.schema.json) for `config.yaml` and
[`schema/project.schema.json`](schema/project.schema.json) for a project
`.ghok.yaml`. Print it with `ghok config schema [--project]`; regenerate with
`go generate ./internal/config` (also available as `make schema`). Curate field
tags in `internal/config/types.go` and starter templates, not generated JSON.
For editor completion, start the YAML file with:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/mkusaka/ghok/main/schema/config.schema.json
```

Global config: `GHOK_CONFIG_DIR/config.yaml`, otherwise
`$XDG_CONFIG_HOME/ghok/config.yaml`, otherwise `~/.config/ghok/config.yaml`.
Missing config defaults to **observe**, unknown-mutation **audit**, and logging
**enabled**. Project policy can only tighten explicitly set mode/unknown handling
and append rules/protected repositories; it never weakens global policy.
Projects cannot set runtime or logging. `trust.include` is project-only; user
configuration has no `trust.projects` setting.

Project discovery stops before `$HOME`, so a dotfiles repository at `$HOME`
does not supply project config. Project read/parse/validation failures discard
only the project overlay and appear in validate/doctor/explain notes and log
diagnostics. Project unknown keys (including nested keys) are ignored with notes;
user config remains strict. An invalid user config declaring
`policy.mode: enforce` in raw YAML prints an error every time, exits 1, and never
starts gh. Observe or undeterminable mode runs gh without hooks/policy with a
warning throttled to once per ten minutes per error. `GHOK_FAIL_CLOSED=1` also
rejects invalid project files and unreadable/unparseable user config; missing
user config still uses defaults. Validate and doctor explain the selected
failure behavior.

Inspect project hooks, then manage their activation:

```sh
ghok config trust                   # current repository-root .ghok.yaml
ghok config trust /path/to/repo/.ghok.yaml
ghok config trust --list             # all retained trusted digests and metadata
ghok config untrust                 # remove only the current digest; policy remains
ghok config untrust --all           # remove every trusted digest
```

Trust retains a set of content digests across versions, worktrees and clones.
The v1 manifest sorts unique repository-relative paths and encodes each path,
executable bit and SHA256 content hash (or missing marker), separated by NULs.
It covers exact `.ghok.yaml` bytes, project hook executables inside the root,
and files explicitly listed under project-only `trust.include`, for example:

```yaml
trust:
  include:
    - .github/scripts/check-pr-description.ts
    - .gitleaks.toml
```

Project hook commands must start with `./` or be absolute; user hooks may still
use bare PATH names. Relative paths resolve under the repository root. Manifest
files must resolve within that root (including symlink targets) and be regular
files, with at most 256 files and total bytes bounded by the effective
`runtime.fileMaxBytes`. Missing files are represented by markers, so creating
one changes the digest. Absolute external executables are not hashed.
Any covered byte or executable-bit change disables hooks for that content until
re-trusted; notes name changed files. Policy-only configs skip manifest computation.

Interpreters, PATH tools, unlisted imports, dependencies and data are not covered.
Add repository-relative imports/dependencies/data to `trust.include` as needed;
trust output lists every hashed file, hook IDs and resolved commands, and explains
these coverage limits.

`<config-dir>/trust.json` stores each digest with `firstPath`, `configPath`,
`trustedAt`, hook IDs and per-file executable bits and hashes/missing markers.
`ghok config trust --list` shows retained digests, first paths, timestamps, hook IDs
and file metadata, plus store notes. Locations are diagnostic metadata, not digest
inputs. The store is written atomically with mode 0600 in a 0700 config directory.
Invalid or old-format stores grant no hook trust and emit a generic note;
declarative policy still applies.

Trust and current-only untrust reject missing configs and paths other than a
repository-root `.ghok.yaml`. Untrust is idempotent and leaves other trusted
versions intact; `--all` clears the entire digest set. Identical code is trusted
anywhere, not a repository identity. This is not a security boundary against
concurrent file replacement or malicious users. Validate, doctor and explain
report trust notes; ordinary wrapper invocations do not print them to stderr.

```yaml
version: 1
policy:
  mode: enforce
  unknownMutation: audit
  protectedRepositories: ["org/repo"]
  protectedUnknown: deny
hooks:
  - id: deny-admin-merge
    phase: pre
    match:
      operation: "pr.merge.*"
    command: ["./hooks/deny-admin-merge"]
    timeout: 5s
    onError: deny
  - id: audit-merge
    phase: post
    match:
      operation: "pr.merge.*"
    command: ["./hooks/audit-merge"]
    timeout: 5s
    onError: ignore
```

Use the compiled Go examples in [examples/hooks](examples/hooks) and follow
[examples/config.yaml](examples/config.yaml) to install them. The pre-hook
denies explicit `admin: true` merge requests and otherwise abstains; it does
not validate every GitHub operation. The audit hook records command outcomes,
not confirmed server-side merge events. A Python equivalent is optional;
Python is not a ghok runtime requirement.

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
usage errors exit 2. Other management commands use 0 success, 1 failure/denial,
2 usage error. `explain --show-secrets` deliberately disables display redaction;
do not paste that output into logs or tickets.

Explain starts with the policy decision, deny reasons and pre/post hook
selection, then shows operations and unresolved fields once, followed by rule
matches, additional policy reasons and config notes. Hook outcomes remain unknown;
an enforce denial prevents gh and post hooks even if their matches are listed.
Normalize keeps its JSON contract. `config validate` and doctor's `config` check
report the global source (exists or not found/defaults) and merged project policy.
`hooks list` and explain show ignored project hooks with `untrusted` or
`changed since trust` reasons and guidance to run `ghok config trust`.
Hook listings include each hook's source config path in text and JSON;
source is not a YAML setting.

## Performance

No matching post-hook and no live stdin pump: exec replaces ghok after checks,
with no lingering supervisor. Observe mode with no hooks/rules and
`logging.enabled: false` also skips normalization, stdin capture, and resolution.
Default no-config behavior does **not** take that no-analysis shortcut: it logs.
Local context is lazy; observe never implicitly queries GitHub. Enforce only
queries for missing fields relevant to enforcing matches, with per-invocation
caching and timeouts.

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

## Technical references

- [Architecture](docs/architecture.md)
- [Hook protocol and execution](docs/hook-protocol.md)
- [Normalization model and registries](docs/normalization.md)
- [Security and trust](docs/security.md)
- [Compatibility and transparency](docs/compatibility.md)
- [Detailed engineering records](docs/notes/)

MIT licensed; see [LICENSE](LICENSE).