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
- `GHOK_NESTED=1` explicitly bypasses normalization/policy/hooks. Hooks receive
  it to avoid recursive evaluation. The depth guard detects runaway activations,
  not unauthorized execution: depths 1–4 are allowed; the fifth/malformed depth
  fails.
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
invocation, exits 1, and never starts gh. This mode is read independently of
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

Repository hooks are inactive until the user inspects them and runs
`ghok config trust [PATH]`. Without PATH it selects the current repository-root
`.ghok.yaml`; an explicit path must designate a repository-root `.ghok.yaml`.
Repository/config symlinks are allowed only when manifest files resolve within
the repository root. Trust validates project restrictions and effective config,
then prints the digest, every hashed file with hook IDs, enabled hooks with
resolved commands, and a coverage disclaimer.

The v1 canonical manifest sorts unique repository-relative paths, encoding each
path, executable bit and SHA256 content hash or missing marker separated by NULs.
It includes exact `.ghok.yaml` bytes, root-contained project hook executables and
project `trust.include` files. Use `trust.include` for repository-relative imports,
dependencies or data, such as `.github/scripts/check-pr-description.ts` and
`.gitleaks.toml`. Existing manifest entries must resolve to regular files inside
the root; escaping paths/symlinks are rejected. The manifest permits at most
256 files and total bytes up to the effective `runtime.fileMaxBytes`.
Policy-only configs skip manifest computation.

Project commands must use `./relative` or absolute executable paths; user hooks
retain bare PATH names. Absolute external executables are excluded from hashing.
Interpreters, PATH tools and unlisted imports, dependencies or data are not
covered. Changing any covered byte (including YAML comments) or executable bit,
or creating a previously missing file, changes the digest and disables hooks
for that content until re-trusted. Notes identify changed files.

`<config-dir>/trust.json` retains a set of digests across versions, worktrees and
clones. Records contain `firstPath`, `configPath`, `trustedAt`, hook IDs and file
executable bits and hashes/missing markers. `ghok config trust --list` prints
digests, first paths, timestamps, hook IDs, per-file metadata and store notes.
Locations are diagnostic metadata, not digest inputs: identical content is
trusted anywhere, not authenticated as a repository identity. The store uses a
0700 config directory and a 0600 temporary file atomically renamed to a 0600
store. Invalid or old-format stores grant no hook trust and return a generic
note; declarative policy still applies.

`ghok config untrust [PATH]` idempotently removes only the current digest without
removing policy; other trusted versions remain. `ghok config untrust --all`
clears all digests. Trust and current-only untrust reject missing configs and
paths other than a repository-root `.ghok.yaml`. Validate, doctor and explain
report trust notes; hook listings/explain identify ignored hooks as untrusted
or changed since trust. Normal wrapper invocations keep trust notes off stderr.

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

Logs default to `<config-dir>/logs/ghok.jsonl` and are enabled even without a
config file. A newly created log directory is 0700; a preexisting possibly
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
Exec-path logs precede execution and cannot include final gh status. Info logs
every invocation; decision logs retain non-allow decisions/errors/hook failures,
not every ordinary nonzero child exit.

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
