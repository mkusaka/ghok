# Security and trust

**ghok is not a security boundary.** It is a user-controlled guardrail and audit
convenience around selected GitHub CLI invocations. Enforce authoritative policy
with GitHub branch protection, repository/organization rulesets, permissions,
and credential scoping. Do not rely on local hooks to stop a malicious user or
untrusted program with your account's access.

## Threat model and bypasses

The intended use is preventing accidental disallowed commands and recording
local intent/outcomes. The user controls the executable, PATH, environment,
config, hooks, and credentials. There is no sandbox, privileged service, token
broker, or server-side enforcement.

- An absolute real-gh path, `curl`, SDK, browser, or another API client bypasses
  the shim. Shell aliases/functions can select a different executable.
- Nested passthrough requires **both** `GHOK_NESTED=1` and an invocation-issued
  `GHOK_NESTED_TOKEN`; the marker alone no longer bypasses normalization/policy/hooks.
  Hooks and resolver gh children share a random 256-bit hex token. Its SHA256
  names an exclusive 0600 file under private 0700 `<config-dir>/state/nested/`.
  Validation rejects malformed tokens, symlinks, nonregular files, and public
  state directories/files. The parent removes the file after its helpers finish,
  before exec or process exit; SIGKILL or failed removal can leave a stale file.
  The same user can create/read this state: it is recursion control, not
  authentication against that user. Depths 1–4 remain allowed; the fifth or
  malformed depth fails.
- `GHOK_REAL_GH` overrides `runtime.realGh`; both are user-controlled absolute
  executable paths. Discovery excludes known wrappers using symlinks/identity
  and state, not code signature verification. An arbitrary renamed different
  wrapper cannot always be identified.
- Real gh generally supplies its stable executable path through `GH_PATH` to
  extensions and configured credential helpers. Those paths can bypass hooks.
  Extensions calling plain `gh` can reenter the shim. Inherited shim-valued
  `GH_PATH` can instead propagate recursion or affect credential-helper setup.
  ghok preserves it, does not repair it, and doctor reports the risk.
- Normalization is bounded, registry-based analysis, not a complete interpreter
  for arbitrary extensions/shell aliases/new gh syntax. Unknown does not mean
  read-only; configure unknown handling according to your risk tolerance.
- A local check can race server-side changes, file replacement, credential
  changes, or concurrent users. Pre-hooks cannot atomically enforce the later
  GitHub operation. A post exit-zero result does not prove a merge occurred.

## Configuration failure modes

An unreadable, malformed, or invalid project `.ghok.yaml` drops **only the
project contribution**. User hooks and policy remain active. Validate, doctor,
and explain report the ignored project; invocation logs retain the failure
diagnostic when logging is enabled. Project unknown keys, including nested keys,
are ignored with notes rather than invalidating known settings. User config
unknown keys remain errors.

An invalid **user config** fails closed when its raw YAML declares
`policy.mode: enforce`: the wrapper prints a redacted error on **every**
invocation, exits 78, and never starts gh. This mode is read independently of
strict decoding/validation. Unparseable YAML, unreadable files, or an absent or
indeterminate mode cannot establish enforce intent; those cases and explicit
observe mode fail open, running real gh without hooks or policy.
`GHOK_FAIL_CLOSED=1` overrides this fallback for **any** invalid config, including
an invalid project file, and prevents gh execution. A missing user config is
valid and still uses defaults. Validate and doctor state the applicable behavior.

Invalid-user-config failures (and forced project failures) are logged at the
default logging path, never one taken from invalid config. Fail-open warnings
for the same `(config path, error)` are printed at most once per ten minutes,
using SHA256-keyed timestamp files under `<config-dir>/state/` (0700 directory,
0600 files, with per-pair process locking). State I/O errors print the warning;
throttling never hides the event from the log. Fail-closed errors are never
throttled. Runtime pre-hook errors default to deny only when valid config
establishes enforce mode; observe records would-deny and still runs gh.

Enforce content denials (rules, policy handling, or hook-returned deny) use
77 (`EX_NOPERM`); hook-`onError`-only denials use 78 (`EX_CONFIG`).
Content denial wins when both occur. `ghok exec -- <gh args>` uses exactly
the shim pipeline, including discovery's shim rejection and the depth/token
checks; it is not a bypass. Hook input and logs identify the entry point in
`invocation.via`.

### GraphQL schema-known writes and unknown handling

GraphQL uses a pinned schema base plus curated semantic overrides. Schema-known
mutations without an override are now family `graphql.mutation`, action
`graphql.<name>`, effect write, with no `unknown-mutation` reason. Previously they
were family `unknown` and `policy.unknownMutation: deny` blocked them; **that
setting alone no longer blocks schema-known mutations**. To deny/audit these
writes, use a rule matching `operation: "graphql.*"` and `effect: write`.
Curated names such as `pr.merge.direct` remain unchanged and require their
existing matches. Snapshot-absent mutations remain unknown writes, including
when aliased to known names. Exact classification does not imply authorization,
safe inputs, schema validity, or resolved repository identity; protected-target
handling still applies independently. See [normalization](normalization.md#schema-backed-default-mutation-fields).

## Hooks are trusted code

Hooks execute as your user, with inherited cwd and environment plus configured
`env` overrides and ghok metadata. They can read credentials, files, keychains,
original payload files, and call APIs independently. **Hook input is raw by
default**, allowing scanners to see the credentials gh will submit. Set
`redactInput: true` for any hook forwarding input elsewhere. Optional redaction
is not a sandbox: inherited GH_TOKEN/GITHUB_TOKEN and other credentials are not
removed from hook environments. Inspect hook code before installing it.

Commands are argv arrays without implicit shell evaluation. Relative executable
paths containing `/` resolve against their originating config directory; bare
names search the hook's effective PATH. Timeout/cancellation kills ordinary
members of the hook process group (TERM then KILL after 500 ms), not descendants
that deliberately leave it. Interactive hooks and escaped-process cleanup are
unsupported. Hook stderr/response diagnostics are redacted before display/logs.

Hook-authored terminal diagnostics are sanitized **after** credential redaction:
controls other than newline/tab, including ESC, BEL, CR, C1 and DEL, and invalid
UTF-8 bytes become visible `\xNN` escapes. Denial entries and other text
diagnostics are bounded to 40 lines/4 KiB, with at most 500 Unicode characters
per line and visible truncation markers. `--show-secrets` does not disable this.
JSON output, hook payloads, logs and real-gh streams are not changed by the
terminal sanitizer. See the [display contract](hook-protocol.md#stable-stderr-denial-contract).

SIGINT/SIGTERM/SIGHUP during resolver/pre-hook execution cancel helper groups
and re-raise the signal after reaping; during post-hooks they cancel/reap hooks
but preserve the completed gh result and log the interruption. Startup-ignored
signals are not watched (subject to Go's documented disposition limits).
**SIGKILL cannot be intercepted: hooks may outlive ghok when the wrapper is
killed with SIGKILL.** Deliberately escaped descendants remain unsupported.

## Project configuration trust

Global path precedence: nonempty GHOK_CONFIG_DIR, XDG_CONFIG_HOME/ghok, then
HOME/.config/ghok. Repository discovery stops **before checking HOME**: a
dotfiles repository at HOME and its `.ghok.yaml` do not become project config
for HOME or unrelated child directories. The nearest discovered repository's
valid `.ghok.yaml` contributes declarative policy: explicitly set values can
only tighten observe to enforce or allow to audit to deny, while rules and
protected repositories append. Attempted weakening is ignored with a note.
Projects cannot set runtime or logging, even empty sections. `trust.include` is
project-only; user `trust.projects` is not supported. Project read, parse,
validation, manifest, or merge failures discard the entire project overlay,
including changes made before a hook/rule collision is detected; they cannot
discard user configuration.

Repository hooks are inactive until inspected and explicitly trusted with
`ghok config trust [PATH]`. PATH defaults to the repository-root `.ghok.yaml`;
an explicit path must designate that file. `--hook ID` is repeatable and trusts
only those hooks, retaining all other hooks' previous digests and review baselines.
Without `--hook`, trust covers all project hooks.

Each hook's v2 digest covers its canonical, defaulted definition (`id`, `phase`,
`match`, `command`, `env`, `timeout`, `onError`, `dependsOn`, `optional`, `enabled`,
`captureOutput`, `redactInput`, `maxOutputBytes`, `background`, `files`) and its
dependency files. Definitions use deterministic JSON; map key order is irrelevant.
YAML bytes, comments, formatting, policy and list position are not inputs.
Express ordering guarantees with `dependsOn`; reordering unrelated hooks does
not revoke trust. Changes to one hook do not disable unaffected siblings.

Dependency files include the repository-local executable, that hook's `files`,
and top-level `trust.include` (applies to **every hook**). Files are hashed once
with all affected hook IDs recorded. Use `files` for hook-specific imports,
dependencies or data; `trust.include` is for shared dependencies.
`hooks[].files` is rejected in user configuration: user hooks run without trust.
Project file paths must be repository-relative (`filepath.IsLocal`), with no
`..` components or globs. Existing files must be regular files, not directories
(list individual files), and symlink targets must stay inside the repository.
The project YAML must also resolve inside the repository, although its bytes are
not hashed. Limits remain 256 unique files and total dependency bytes up to
effective `runtime.fileMaxBytes`. Missing files have markers; creating one or
changing a file's bytes/executable bit revokes trust for its users.
Trust review warns about missing gate dependency files.

A **gate** is an enabled pre-hook with effective `onError: deny` (the pre default).
If its recorded digest differs, matching invocations fail closed in enforce mode
with `ghok: denied by config <id>` and exit 78. An untrusted gate also fails closed
when another hook from the project is trusted and enabled. Observe mode records
would-deny instead; a never-trusted project still ignores hooks with a throttled
warning and no gate denial. The reason directs the user to `ghok config trust`.
Rules and policy remain trust-free.

Project commands require `./relative` or absolute executable paths; user hooks
retain bare PATH names. Absolute external executables, interpreters, PATH tools
and unlisted imports/dependencies/data are not hashed. Policy-only configs skip
manifest computation.

### Transform trust and failures

`transforms.pre` executes trusted code with the same privileges, raw-input
default, environment, timeout/cancellation and nested-call token as hooks.
`redactInput: true` masks only its input copy, not inherited credentials.
Project transforms use the same per-command digest, executable/dependency-file
coverage, `files`, `trust.include`, review and ignored-command warnings as hooks.
Their IDs cannot collide with hook IDs. Untrusted or changed transforms are
inactive and **never gates**, even alongside trusted project commands.

Transforms run before final policy and pre-hooks, so those checks judge the
request that will actually execute. A malformed/unsupported edit or transform
process failure with `onError: deny` stops gh with 78 **even in observe mode**.
With `onError: allow`, all preceding changes are rolled back, the remaining
transforms are skipped, and normal checks judge the original request.

Write-back accepts only restricted paths and sources and verifies a semantic
round trip. It does not authorize arbitrary operation changes. File/stdin edits
use private 0600 temporary transport; original files are never overwritten.
Named transports are removed after the child exits, and failed applications
clean up immediately. SIGKILL can leave temporary files, just as it can leave
other wrapper-owned state. Logs contain redacted transform outcomes/summaries,
not replacement prose. These controls do not sandbox trusted transform code.

Dependency file hashes are cached in
`<config-dir>/state/manifest-cache.json` (0600, atomic replacement; 0700 state
directory). The cache retains at most 4096 entries across projects (8 MiB maximum),
evicting the oldest computed hashes on overflow. Hits require identical canonical
path, size, nanosecond mtime, device/inode, ctime and mode on macOS/Linux;
unsupported platforms rehash.
Corrupt, oversized, or nonprivate cache files are ignored. YAML is always read
and parsed fresh; containment, regular-file, executable-bit and total-byte checks
still run on cache hits. Warm hits do not rewrite cache state.
Like the trust store, this is not protection against malicious same-user state
changes or concurrent filesystem replacement.

`<config-dir>/trust.json` holds **one record per repository** (keyed by the
resolved Git common directory; outside Git, the canonical project path),
containing hook ID → digest and canonical definitions, plus `firstPath`,
`configPath`, `trustedAt`, hook IDs and per-file executable bits/hashes/missing
markers. HEAD `commit`, resolved Git `commonDir` and normalized credential-free
origin `remoteUrl` are diagnostic provenance, never digest inputs. A hook is
trusted when its digest appears in any record (current or `digests`, the last 128
earlier grants of that repository), so identical worktrees and clones share
trust; comparison heuristics never authorize hooks. A changed gate is judged
against the repository's last grant in every worktree.
Selective trust retains old file snapshots/blobs for unselected hooks, even when
a shared path now has different trusted bytes for different hooks.
Old manifest records are ignored safely and require re-trust after upgrading.
The store uses a 0700 config directory and atomic 0600 temporary-file replacement.
Malformed entries are skipped individually with a named note; valid entries stay
trusted. Invalid/unreadable top-level stores grant no trust. Policy still applies.

Review first uses this repository's record; otherwise it selects the newest record
in the first matching tier: resolved Git common directory, `firstPath`, normalized origin URL, then at least
two distinct shared file-content SHA256s, independent of filenames. One common
include such as `.gitleaks.toml` is insufficient. These heuristics choose review
hints only; they never authorize hooks. Remote normalization lowercases hosts,
strips userinfo, ports 22/443 and trailing `/`/`.git`, and maps scp-like/SSH/HTTPS
URLs to `host/owner/repo`; path case is retained and `insteadOf` is ignored.
HEAD/refs/config are read from files without invoking git; HEAD is provenance
for `trust --list`, never a diff baseline. No Git objects or network are needed.
Trust review groups changed definitions/files by hook and shared file diffs once.
Validate/doctor also identify per-hook changes and inactive gates. Mode labels
`0644`/`0755` describe executable bits, not full permissions.
Only `config trust` and `config trust --dry-run` render unified content diffs.
The displayed diff masks credentials with the same redactor as `explain`
(`--show-secrets` shows it raw); `--no-diff` suppresses it. Dry-run writes no trust,
blob or hash-cache state.

Trust saves **exact, unredacted copies of trusted files**, including uncommitted
contents, at `<config-dir>/trust/blobs/<sha256>`. These copies may contain secrets,
just like the repository files. Config/trust/blob directories are 0700 and blob
files are 0600; write-once temporary-file/rename publication deduplicates contents.
SHA256 is verified on read; a corrupt copy reports `stored copy corrupt` and
produces no diff. Files over 1 MiB stay hash-only (`no stored copy (over 1 MiB)`).
At 64 MiB total retained copies, new blobs are skipped with a note; existing
copies still work. Trust and untrust collect copies referenced by no trust entry
in one directory walk; **`ghok config untrust --all` purges all stored copies**.
Displayed diffs are credential-masked unless `--show-secrets` is explicitly used.
Treat their output as sensitive; do not attach it
to tickets without inspection. NUL in the first 8 KiB produces a binary-change
hash summary; over 20,000 lines per side produces `diff too large; files changed`.
Other bytes are diffed as-is, splitting on LF only and displaying CR as `^M`.

`ghok config untrust [PATH]` idempotently removes the whole project's record,
without changing policy; `--all` clears every project record and purges blobs.
Trust/untrust reject missing configs and non-repository-root `.ghok.yaml` paths.
Validate, doctor, explain and hook listings report each hook's `untrusted` or
`changed since trust` reason, explicitly naming inactive gates. The wrapper warns
about enabled ignored hooks once per canonical project path/current ignored-hook
IDs, digests and reasons per ten minutes, using the private locked warning state.
Changed-file lists show at most three names, followed by a remaining count.
Set user `logging.ignoredHooksWarning: false` to opt out. Denied, invalid-config,
verified nested-token, disabled-hook-only and policy-only paths do not warn.

Group/world-writable configs and hook files produce notes, not a mandatory
refusal. No ownership/signature check authenticates hook code. Content hashes
and atomic writes are not a security boundary against concurrent filesystem
replacement, competing writers or malicious users.

## Redaction rules and limits

The CLI creates display copies for logs, normalize/explain, errors, and reasons.
Hook JSON is raw unless that hook opts into `redactInput: true`. Real gh gets
original arguments/environment/request bytes; redaction must never rewrite API
requests or stdout/stderr.

| Rule | Coverage |
| --- | --- |
| Learned environment values | All nonempty GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, GITHUB_ENTERPRISE_TOKEN, even short values; other names containing TOKEN/SECRET/PASSWORD/PASSWD/API_KEY/PRIVATE_KEY/CREDENTIAL when value is at least 8 bytes. |
| Token patterns | Classic `gh[pousr]_` with at least 36 suffix characters, `github_pat_` with at least 20, `v1.` plus 40 hex characters, JWT-like `eyJ` strings. |
| Credentials in strings | Bearer/token/Basic authorization values; Authorization/Proxy-Authorization headers; URL passwords; complete or unfinished PEM private-key blocks. |
| Sensitive field names | token/accessToken/refreshToken/idToken/authToken, GitHub token names, password/passwd/secret/clientSecret/apiKey/privateKey/credential/credentials/authorization/proxyAuthorization; ASCII case, underscores/hyphens, dotted/bracket paths normalized. |
| argv context | Sensitive flags, attached/equals/separate headers/API fields, bundled API flags, `secret set --body/-b`; `--` ends contextual interpretation, not learned/pattern scrubbing. |
| GraphQL | AST-based sensitive arguments/input/defaults and learned/pattern strings; display reformats and drops comments. Invalid/truncated GraphQL is withheld. |
| Captured hook bodies | Decode/redact/re-encode base64; malformed base64/structured bodies withheld; original size/truncation metadata retained. |

The usual marker is `[REDACTED]`; a credential contained inside that marker is
removed too. Longest-first replacement plus removal handles overlapping learned
values. JSON number precision is preserved.

Limits:

- Unknown secrets under ordinary field names and unrecognized encodings can
  remain visible. Arbitrary binary/encrypted/base64 secrets are not inferred.
- `key`, `keyboard`, `publicKey`, and `tokenizer` are not sensitive names.
  `variable set` bodies are ordinary data, unlike secret-set bodies.
- Redaction operates on JSON-like values, not arbitrary application structs,
  pointers, or cyclic object graphs. Pure normalizer output remains raw until
  the caller applies redaction; Source contract comments are not a guarantee.
- Original gh output is never redacted. A gh command itself can print secrets
  to your terminal or redirected output. Hook code can expose inherited tokens.
- `explain --show-secrets` intentionally displays unredacted analysis; treat
  that output as sensitive and do not attach it to tickets.

## Files, permissions, and logging

Recommended user-created config mode: 0600; hook/config directories: 0700;
executable hooks: 0700. Trust-store commands create/harden their config directory
and store as described above; this does not harden your hook programs.
`ghok shim install` creates a missing bin directory as 0755 (subject to umask), an owned
symlink, and shim state. Newly created state directories are 0700 and atomic
state files 0600; existing directory permissions remain unchanged. Disable
removes only owned shims, never real gh or foreign files. Concurrent external
shim/state mutation is unsupported.

Without a user config or applicable project config, the wrapper execs real gh
without opening logs or warning state, normalizing, spooling stdin, or resolving.
With any config, logs default to `<config-dir>/logs/ghok.jsonl` and are enabled.
A newly created log directory is 0700; a preexisting possibly
shared directory is left unchanged. Log and persistent `.lock` files are opened
as regular files with modes tightened to 0600. Final-directory/file symlinks
and nonregular log/lock files are rejected; ancestor symlinks are allowed.
Secure stdin spool files are 0600 and immediately unlinked.

JSONL contains invocation ID/time/duration, redacted argv/operations,
classification/unresolved evidence, policy/hook results, diagnostics, and
execution metadata when supervised. **Captured gh output bodies are omitted.**
Each record includes `redactions`: credential substitutions made by the log
redactor, not unique secrets or content summaries. Repeated representations may
count separately; zero is not proof of secret-free input.
Exec-path logs precede execution and cannot include final gh status after a
successful exec. If exec returns, a second record with the same invocation ID
records `exec-error`, `exec.startError`, and the shell-style exit code (126/127).
Info logs every invocation; decision logs retain non-allow decisions/errors/hook
failures, not every ordinary nonzero child exit.

**Content minimization is log-only.** Known prose flags/keys (`body`, `title`,
`message`, `description`, `subject`, `commitHeadline`, `commitBody`, `comment`,
`notes`) are replaced with `[content: N bytes]`, even for short single words.
Other argv values and nested operation/source strings containing Unicode
whitespace or longer than 64 bytes get the same marker. Structural flag names,
command words, repositories/owners, branches/refs, URLs/hosts, numbers,
actions/families, and short enum-like values remain readable, subject to
credential redaction. API field syntax retains the key, not its prose value.
GraphQL query strings/documents become
`[graphql document: N bytes, sha256:<16 hex digits>]`, measured and hashed before
credential redaction; source fingerprints cover the normalizer's bounded copy.
Byte counts/fingerprints reveal length and permit matching identical content;
they are not encryption. Hooks retain raw content by default; normalize/explain
and `redactInput: true` hooks retain ordinary content with credential redaction.

Routine config-source notes are not log diagnostics; ignored-project failures
are. Deliberately unexamined
API stdin is logged as `allow` with
`stdin not analyzed (nothing could act on it)`, retaining the unknown operation
and analysis errors; this is passthrough, not a classification as safe/read.
An applicable enforcing policy/pre-hook still triggers the existing analysis.

A mutex and persistent-file flock serialize cooperating writers; the append
file is reopened under lock after rotation. Logs rotate once an existing file
exceeds 10 MiB, retaining `.1`–`.3`; oversized records remain whole. No fsync or
crash-durability guarantee; network-filesystem flock semantics are not promised.
Do not remove the lock file while writers may be active. Logging failures never
change gh execution; only GHOK_DEBUG=1 exposes them on stderr. This is not a
reliable tamper-proof audit trail.

## Supply chain

Actions are pinned by full commit SHA with version comments. CI grants only
contents:read; the release job alone grants contents:write, id-token:write, and
attestations:write. Release tar archives/checksum manifest are attested, and
binaries use CGO_ENABLED=0, -trimpath, explicit version/commit, and disabled
implicit VCS metadata. Tar ownership/timestamps and gzip metadata are normalized.
The host image/toolchain/dependency sources remain trusted inputs; these flags
are not proof of bit-for-bit reproducibility across arbitrary Go versions/hosts.

Verify the complete checksum manifest and the selected archive provenance as
shown in [README](../README.md#release-archive). Private-repository attestation
availability depends on GitHub plan/feature support; attest failure stops the
workflow before release publication rather than silently publishing unattested
assets. Releases are not notarized or Developer ID signed.

## Evidence

Implementation: `internal/config/project.go`, `internal/redact/`,
`internal/logging/logger_unix.go`, `internal/hooks/execute_unix.go`,
`internal/install/`, and `internal/cli/wrapper_unix.go`.
Detailed engineering records: [configuration/redaction/logging](notes/config-logging-redaction.md),
[installation](notes/install.md), and [hooks/policy](notes/hooks-policy.md).
Upstream GH_PATH/helper behavior was verified there against cli/cli 2.102.0
[`internal/ghcmd/cmd.go`](https://github.com/cli/cli/blob/v2.102.0/internal/ghcmd/cmd.go),
[`pkg/cmd/extension/manager.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/extension/manager.go),
and [`pkg/cmd/auth/shared/gitcredentials/helper_config.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/auth/shared/gitcredentials/helper_config.go).
