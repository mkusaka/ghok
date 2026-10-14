# Config, logging, and redaction

## Configuration

- `Dir(env)` uses nonempty `GHOK_CONFIG_DIR`, then `XDG_CONFIG_HOME/ghok`, then `HOME/.config/ghok`. Environment lookup uses the supplied map, not the process environment. `DefaultPath` appends `config.yaml`.
- `Load` supplies defaults for a missing file and stores its absolute containing directory in `Config.Dir`. Nonempty YAML requires `version: 1`; empty files, comments, null documents, and empty mappings use version 1. Explicit numeric zero limits remain errors. Only one YAML document is accepted. yaml.v3 `KnownFields(true)` rejects unknown fields with line numbers. `Validate` joins independent validation failures with indexed field paths.
- Defaults are exported constants: observe mode; unknown mutation audit; protected unknown deny; hooks enabled, timeout 10s (maximum 10m), pre onError deny, post onError ignore, maxOutputBytes 1 MiB; runtime concurrency 1, stdin/file limit 8 MiB, stdin timeout 30s, capture limit 1 MiB, resolve timeout 5s; logging enabled, info level, `logs/ghok.jsonl` beneath Config.Dir.
- Hook and rule IDs must be unique; hook dependencies must exist, have the same phase, and form an acyclic graph. Post hooks cannot deny on error; only post hooks can capture output. Match globs use `path.Match` syntax; repository patterns require owner/name shape.
- `HookCommandPath` resolves relative executable paths containing `/` against Config.Dir; bare names remain PATH lookups. Command arguments are unchanged.
- `LoadEffective` walks upward from the symlink-resolved cwd to the nearest `.git` file/directory, stopping at HOME or the filesystem root. It considers only that repository's `.ghok.yaml` and always parses and merges its declarative policy.
- Only explicitly supplied project policy values participate in tightening: observe < enforce and allow < audit < deny. Rules and protected repositories append; attempted weakening is ignored with a note. Any project runtime, logging, or trust section is an error, including empty sections and merged YAML keys. Active hook/rule collisions fail validation.
- Project hooks activate only when the canonical absolute, symlink-resolved config path and SHA256 of its exact bytes match `<config-dir>/trust.json`. Entries also record `trustedAt`. Any byte edit revokes hooks only; `Config.Hooks` contains active hooks, while `Config.IgnoredHooks` records `untrusted` or `changed since trust` reasons.
- `TrustProject(path, cwd, env)` validates repository-root `.ghok.yaml`, project restrictions and effective config, writes the trust record, and returns canonical path, enabled hooks with resolved commands, and notes. `UntrustProject(path, cwd, env)` removes the record idempotently; both reject missing/outside-repository files. Empty path selects the current repository root. File/repository symlinks are allowed.
- Trust writes use a 0700 config directory and 0600 temporary file plus atomic rename to `trust.json`. Corrupt reads return a note and empty hook trust, without suppressing project policy. Legacy `trust.projects` parses but is ignored with a deprecation note, even when empty.
- Relative project hook paths resolve against the repository config's containing directory, even when the config itself is a symlink. Merged project hook executable paths are made absolute to preserve their origin. Hook executable contents are not hashed.
- Group/world-writable config and hook files produce notes. Bare hook names are inspected through the supplied PATH; missing executables are left for the runner/doctor. Packages return notes without printing them; validate/doctor/explain present them, while ordinary wrapper stderr stays quiet.
- Hooks are explicitly trusted code. Trust/path hashes and atomic writes are not a security boundary against concurrent filesystem replacement, competing writers or direct GitHub API access.
- Schemas use the local standard-library reflection generator, including `deprecated:"true"` field tags. Curate `types.go` tags and templates, not generated JSON; regenerate with `go generate ./internal/config`. No new upstream snapshot or generator dependency is needed.

## gh aliases: verified source behavior

Read-only source verification against [cli/cli v2.102.0 `internal/config/config.go`](https://github.com/cli/cli/blob/v2.102.0/internal/config/config.go): `NewConfig` delegates reading to cli/go-gh; `AliasConfig.Get/All` read the top-level aliases map without expanding it. [cli/cli v2.102.0 `go.mod`](https://github.com/cli/cli/blob/v2.102.0/go.mod) pins cli/go-gh/v2 v2.16.1. [cli/go-gh v2.16.1 `pkg/config/config.go`](https://github.com/cli/go-gh/blob/v2.16.1/pkg/config/config.go) verifies `ConfigDir` precedence and `generalConfigFile` naming.

`LoadGhAliases` reads `config.yml` from GH_CONFIG_DIR > XDG_CONFIG_HOME/gh > HOME/.config/gh. Missing files yield an empty map. Plain and `!`-prefixed shell aliases are preserved verbatim; expansion/shell classification is the normalizer's responsibility. Unrelated gh configuration keys are accepted. This reader does not read credentials from hosts.yml or the OS keychain, write gh configuration, or invoke gh.

## Secret redaction

- `New(env)` snapshots nonempty GitHub credential values (GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, GITHUB_ENTERPRISE_TOKEN). Other environment names containing TOKEN, SECRET, PASSWORD, PASSWD, API_KEY, PRIVATE_KEY, or CREDENTIAL contribute values only when at least eight bytes long.
- All learned values match anywhere. Pattern coverage includes classic GitHub token prefixes with at least 36 suffix characters, github_pat_, v1. plus 40 hex characters, JWT-like eyJ tokens, Bearer/token/Basic authorization values, Authorization/Proxy-Authorization headers, URL passwords, and complete or unfinished PEM private-key blocks.
- Marker: `[REDACTED]`. In the pathological case that a learned credential is part of the marker itself, the marker is scrubbed too; credential removal takes precedence over retaining that literal marker. Longest-first replacement and a terminating removal pass prevent overlapping or newly joined secret values from surviving.
- Sensitive names: token, accessToken, refreshToken, idToken, authToken, ghToken, githubToken, ghEnterpriseToken, githubEnterpriseToken, password, passwd, secret, clientSecret, apiKey, privateKey, credential, credentials, authorization, proxyAuthorization. Matching ignores ASCII case, underscores, and hyphens; bracket/dotted field paths inspect components. `key`, `keyboard`, `tokenizer`, and `publicKey` are not sensitive names.
- `Argv` handles separate/attached/equals header and API field flags, API boolean shorthand bundles, --password/--token and other sensitive-name flags, and `secret set --body/-b`. `variable set` bodies remain ordinary data. `auth login --with-token` has no argv token to redact. `--` ends contextual flag interpretation; learned-value/pattern redaction still applies to every string.
- `Value` copies JSON-like maps/slices, including common typed containers and JSON byte bodies. Numeric `json.Number` values retain their precision/type. `Result` and `Input` copy every mutable contract field and redact every string, including errors, metadata, URL credentials, and resolved context.
- GraphQL redaction uses gqlparser ASTs to scrub sensitive argument/input/default values and learned/pattern strings. Display copies are reformatted and comments omitted. Malformed/truncated GraphQL is withheld rather than emitted unparsed. These copies are never execution/replay input.
- Captured hook bodies are decoded, redacted, and re-encoded; JSON decoding preserves numeric precision. Invalid base64 or malformed structured bodies are withheld. Original byte-count/truncation metadata remains execution metadata.
- Unsupported: arbitrary application structs/pointers, cyclic non-JSON graphs, and credentials in unrecognized encodings. Ordinary binary bodies retain their nonsecret bytes. Redaction cannot infer arbitrary sensitive values under ordinary field names when they were neither learned nor pattern-shaped.

## JSONL logging

- API: `Open(path, level)`, `OpenWithRedactor(path, level, redactor)`, `Write(Record)`, `Close`, and `NewInvocationID`. Open and a nil injected redactor snapshot the process environment. Logger.Write always redacts a copy; callers need not remember a separate redaction step.
- Record fields use lowerCamelCase JSON. Zero timestamps become current UTC. Invocation IDs are crypto/rand 128-bit lowercase hex strings.
- A log directory created by ghok gets mode 0700; a pre-existing directory is left untouched (it may be shared). Log and persistent `.lock` files are 0600. Symlink final directories/log/lock files and nonregular files are rejected. Ancestor symlinks are permitted (including macOS /var and /tmp aliases).
- A mutex serializes each Logger's goroutines; flock on the persistent `<path>.lock` serializes cooperating processes. The append file is reopened under that lock so rotation cannot leave writers using stale data descriptors. Each JSON record plus newline uses one append write syscall. Never remove the lock file while writers may exist.
- An existing file larger than 10 MiB rotates before the next write, retaining `.1`, `.2`, `.3`. A record crossing the limit stays intact and rotates on a subsequent write; oversized records are not split. No fsync/crash-durability guarantee; filesystem failures are returned.
- Info logs every invocation. Decision logs exclude allow/passthrough records unless normalization errors, execution start errors, or hook failures are present. Normal child nonzero exit status alone is not a hook failure.
- Exec.Stdout/Exec.Stderr captured bodies are always omitted. Hook stderr, policy reasons, errors, diagnostics, argv, operations, and execution metadata are redacted.
- No stdout/stderr output. Integration must ignore logger failures for gh execution; it may print logger errors to stderr only when `GHOK_DEBUG=1`. The package returns errors and does not implement that CLI presentation policy.

## Verification and platforms

Config, redaction, and logger suites run hermetically with temporary fixtures. Logger tests exercise concurrent goroutines and pipe-synchronized subprocesses, including rotation without stale appends. Redaction has a learned-secret nonleak fuzz target and Result benchmark; logging has a Write benchmark. A throwaway executable exercised trusted config merging, immutable/redacted hook JSON, and credential-safe/no-capture JSONL through the public APIs, then was removed.

Project-trust unit/e2e coverage exercises automatic policy, trust/edit/untrust,
corruption, canonical symlink paths, legacy-key diagnostics and store permissions.
An actual CLI/wrapper fixture confirmed hooks never run before trust, run after
trust, stop after a comment-only YAML edit, and resume after re-trust; policy
denials remain active throughout and allowed wrapper calls emit no trust stderr.
`FuzzTrustStore` completed a 30-second run with 5,414,968 executions without a
failure; corrupt entries must never return partial trust.

Config and redaction use platform-neutral Go APIs. Logging's descriptor, O_APPEND, O_NOFOLLOW, O_NONBLOCK, and flock code is Unix-build-tagged; macOS/Linux are the supported platforms. Tests were executed on macOS arm64. Linux amd64/arm64 cross-compilation is checked separately; Linux runtime execution remains the CI matrix's responsibility. Cooperating local-filesystem flock semantics are assumed; network-filesystem lock behavior is not guaranteed.
