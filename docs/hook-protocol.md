# Hook protocol (schemaVersion 1)

A hook is a **trusted non-interactive executable running as your user**. It reads
one JSON document plus newline and EOF from stdin, then writes at most one JSON
response to stdout. There is no implicit shell. Write diagnostic text to stderr,
not stdout. Hook input is raw. Your hook is now responsible for what it does with secrets; use redactInput: true if it forwards input.
Hooks already inherit credentials such as GH_TOKEN; optional input redaction is
not a sandbox. See [security](security.md).

Pre-hook decisions can stop gh only in enforce mode. Post-hooks receive command
outcomes and never change gh's exit status. A successful command is not proof
of a server-side merge or other side effect.

## Input schema

Contract: `internal/protocol/protocol.go` and `internal/model/`. Non-optional
fields are serialized even for zero values; optional fields below use omitempty
and may be absent. JSON arrays can be null when their Go slice is nil; consumers
should not require an empty array instead. Integers are JSON numbers; captured
bytes are base64, never an assumed UTF-8 string.

### Top-level Input

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| schemaVersion | integer | no | Always 1, set by runner. |
| event | string | no | `pre` or `post`, set by runner. |
| eventId | string | no | Invocation correlation ID, shared across hooks/phases. |
| hookId | string | no | Current configured hook ID. |
| redactions | integer | no | Credential substitutions made in this hook's input; zero for raw input. Counts repeated representations separately, not unique secrets. |
| invocation | InvocationInfo object | no | Original/expanded argv and execution context. |
| classification | Classification object | no | Invocation-level normalization identity/resolution/proof. |
| operations | Operation array/null | no | All normalized operations, including execution evidence. |
| errors | string array | yes | Nonfatal normalization problems; raw unless redactInput is enabled. |
| context | ResolvedContext object | yes | Resolver context when requested. |
| policy | PolicyInfo object | yes | CLI policy evaluation, not aggregate pre-hook decision. |
| result | ExecResult object | yes | Real gh outcome in post phase; absent before execution. |
| previous | HookResult array | yes | Direct dependsOn results in configured dependency order. |

### InvocationInfo

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| argv | string array/null | no | Original arguments after argv[0]; raw unless redactInput is enabled. |
| alias | string | yes | First expanded plain alias name. |
| expanded | string array | yes | Alias-expanded analysis argv; raw unless redactInput is enabled. Real gh still receives original argv. |
| cwd | string | no | Invocation working directory. Hook inherits this cwd. |
| isTTY | boolean | no | Original stdout is a terminal. Capture can make child stdout non-TTY. |
| stdinTTY | boolean | no | Original stdin is a terminal. |
| realGh | string | yes | Discovered real executable path. |
| tokenKind | string | yes | Kind of env token gh will use: `app` (`ghs_`, GitHub App installation, e.g. gain or Actions), `user` (`ghp_`/`gho_`/`ghu_`/`github_pat_`), `unknown`, or `none` (gh's stored login). The token itself is never included. |

### Classification and operation records

| Classification field | Type | Values / meaning |
| --- | --- | --- |
| level | string | `none`, `L1`, `L2`, `L3`. |
| confidence | string | `exact`, `heuristic`, `none`: identity, not server outcome. |
| resolution | string | `complete`, `partial`, `deferred`, `unresolved`. |
| mutationFree | boolean | True only when analysis proves no considered write; unknown is not safe. |

Each Operation has these fields:

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| family | string | no | Related intent, e.g. `pr.merge`; `unknown` for unknown family. |
| action | string | no | Specific action matched by operation globs. |
| effect | string | no | `read`, `write`, `unknown`. |
| target | Target object | no | Known selector metadata; unknown values omitted. |
| parameters | object | yes | Action-specific JSON-like parameters; raw unless redactInput is enabled. |
| texts | Texts object | yes | Normalized title/body/notes and input provenance; existing parameters remain unchanged. |
| execution | string | no | `will-run`, `maybe`, `not-selected`, `skipped`. |
| evidence | string | yes | Proof/explanation of execution state. |
| unresolved | Unresolved array | yes | Needed values that analysis could not establish. |
| unknownReason | string | yes | Machine-readable uncertainty reason. |
| detail | string | yes | Human-readable explanation. |
| origin | Origin object | no | Syntax linking operation to command/route/field. |

Target fields are all optional: host, owner, repository (`owner/name`), nodeId,
branch, baseBranch, headBranch, sha, ref, url are strings; number is an integer
pointer serialized as a number when known. Unresolved records have required
string field/reason and optional string source. Origin fields are optional:
command is a string array; method, route, field, alias, operation and opType
are strings. opType describes query/mutation/subscription; field is the schema
field, not a response alias. Exact meanings, action tables and unknown-reason
values: [normalization](normalization.md).

### Normalized texts

`operations[].texts` is additive in schemaVersion 1. Its optional `title`, `body`,
and `notes` fields each contain a Text object. Absent means no corresponding
text was supplied or identified; an explicitly empty inline string is present.
Merge commit subjects/headlines use `title`, commit bodies use `body`; release
notes use `notes` (including REST release `body`).

| Text field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| value | string | no | Raw available prose, or credentials masked for redactInput hooks; empty when unavailable/generated. |
| source | string | no | `inline`, `file`, `stdin`, `editor`, `fill`, `template`, or `unknown`. |
| path | string | yes | Input file path as supplied, when applicable. |
| status | string | yes | Unavailable native stdin: `not-captured`, `tty`, `too-large`, `timeout`, or `error`; omitted on successful capture. |
| truncated | boolean | yes | Analysis hit a known size limit; do not treat the available value as complete. |
| bytes | integer | no | Original text byte count before redaction, not character count. For truncated native wrapper input, full regular-file size (remaining bytes for redirected stdin) when known; zero if unavailable/unknown. |
| redactions | integer | no | Credential substitutions in value; zero when unchanged or input is raw. |

For example, `gh pr create -t T -b B` supplies:

```json
{"texts":{"title":{"value":"T","source":"inline","bytes":1,"redactions":0},"body":{"value":"B","source":"inline","bytes":1,"redactions":0}}}
```

`--body-file path` uses the bounded injected file reader. Native `--body-file -`,
`-F -` (including bundled shorthands), and `--notes-file -` request stdin
capture only when an enabled pre-hook could match. Capture uses the existing
bounded spool and replays every stdin byte to gh unchanged, including the
unread remainder after a limit or timeout. TTY stdin is never read.

Unavailable file/stdin values retain provenance and add `unresolved` for
`texts.body`/`texts.notes`; native stdin also carries `status`. For example,
over-limit piped input reports
`{"value":"","source":"stdin","status":"too-large","truncated":true,"bytes":0,"redactions":0}`:
the stream's full size is not known before gh starts. **Secret-scanning hooks
should deny when `truncated` is true**, and reject other unavailable inputs if
complete inspection is required. Editor, web, fill, and template flows do not
claim to know their final submitted text.

Hooks receive raw prose by default so secret scanners inspect the same credentials
gh will submit. For hooks that forward input, set `redactInput: true` in that hook's
configuration; only that hook's copy is redacted, including original/expanded argv,
parameters, texts, and captured bodies. Every Text includes `redactions`, including
zero, and the input has a top-level total. Counts measure actual substitutions,
not unique secrets; repeated argv/expanded/text representations count separately.
Deny when `redactions > 0` if masked content prevents safe evaluation; zero is
not proof that content contains no secrets. Hook stderr/reason/message stay
redacted before display or logging regardless of redactInput.

JSONL logs replace every text value with `[content: N bytes]` (available value's
byte count before redaction), retain source/path/status/original-size metadata,
and report their own top-level `redactions` count. Content summarization is not a
credential substitution. See [normalization](normalization.md#normalized-texts)
for per-layer mappings and limits. Texts describe input available before execution,
not GitHub's stored content or an editor's eventual result.

### ResolvedContext and PolicyInfo

| Context field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| host, owner, repository, branch, headSha, gitDir | string | yes | Locally resolved context; not automatically every operation's target. |
| sources | object of string values | yes | Provenance by field (e.g. env/git/default); Target itself has no provenance map. |
| unresolved | Unresolved array | yes | Context uncertainty, including ambiguous candidates. |

| Policy field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| mode | string | no | `observe` or `enforce`. |
| decision | string | no | `allow`, `deny`, `abstain`, or policy-only `audit`. |
| reasons | string array | yes | Evaluation reasons/evidence, not only deny reasons. |
| rules | string array | yes | Matched rule IDs in configuration order. |

### ExecResult (post only)

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| exitCode | integer | no | Normal gh code; -1 for signaled/unstarted execution. |
| signal | string | yes | Signal name such as `SIGINT`. |
| coreDump | boolean | yes | Child wait-status core-dump flag; wrapper core flag can differ. |
| durationMs | integer | no | Execution duration in milliseconds. |
| stdout, stderr | Output object | yes | Only delivered to hooks requesting captureOutput. |
| startError | string | yes | Real gh could not start. |

Output has required base64 (string retained bytes), size (integer total observed
bytes), truncated (boolean). Bodies are raw by default. With `redactInput: true`,
bodies are decoded, redacted and re-encoded; malformed structured data may be
withheld. Size/truncation always describe original capture. No captured bodies
go to logs.

### HookResult (previous dependencies/diagnostics)

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| hookId | string | no | Configured ID. |
| phase | string | no | pre/post. |
| outcome | string | no | Execution outcome below. |
| decision | string | yes | Response decision, meaningful for outcome=ok. |
| reason, message | string | yes | Redacted response/execution explanation. |
| effective | string | yes | Decision used after phase/onError handling. |
| exitCode | integer | no | Hook process status, -1 if unstarted/signaled. |
| durationMs | integer | no | Hook execution duration. |
| error | string | yes | Redacted spawn/wait/timeout/response error. |
| stderr | string | yes | Bounded, redacted diagnostic stderr; may contain truncation marker. |

## Response schema and parsing

```json
{"schemaVersion":1,"decision":"deny","reason":"admin merge forbidden","message":"Use the normal merge workflow."}
```

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| schemaVersion | integer | no | Absent/0/1 accepted; any other nonzero version invalid. |
| decision | string | yes for nonempty response | Only `allow`, `deny`, `abstain`; policy-only `audit` is not a hook decision. |
| reason | string | no | Recorded explanation; fallback denial diagnostic. |
| message | string | no | User-facing denial message on stderr. |

Exit zero with empty/whitespace stdout yields ok/abstain. Nonzero exit is error
regardless of stdout. Unknown JSON object keys are currently ignored, not
strictly rejected. Multiple documents, trailing garbage, invalid JSON/types,
unknown decisions/versions, and stdout overflow produce invalid-response.
Overflow invalidates even an otherwise valid retained JSON prefix: unseen bytes
cannot establish a valid single document. Stderr alone is not a failure.

## Example pre event

Illustrative input for an explicitly targeted admin merge (irrelevant optional
fields omitted):

```json
{
  "schemaVersion": 1,
  "event": "pre",
  "eventId": "8e861e11111111111111111111111111",
  "hookId": "deny-admin-merge",
  "invocation": {
    "argv": ["pr", "merge", "123", "--admin", "-R", "org/repo"],
    "expanded": ["pr", "merge", "123", "--admin", "-R", "org/repo"],
    "cwd": "/work/repo", "isTTY": false, "stdinTTY": false,
    "realGh": "/usr/bin/gh"
  },
  "classification": {"level":"L1","confidence":"exact","resolution":"complete","mutationFree":false},
  "operations": [{
    "family":"pr.merge", "action":"pr.merge.direct", "effect":"write",
    "target":{"host":"github.com","owner":"org","repository":"org/repo","number":123},
    "parameters":{"admin":true}, "execution":"will-run",
    "origin":{"command":["pr","merge"]}
  }],
  "policy":{"mode":"enforce","decision":"allow"}
}
```

`allow` from one hook is not permission to bypass another denial. Hook code must
check every relevant operation, not only operations[0].

## Example post event

A capture-requesting audit hook after a read-only command exited 42 (the same
Input envelope/schema is used):

```json
{
  "schemaVersion": 1, "event": "post",
  "eventId": "8e861e11111111111111111111111111", "hookId": "audit",
  "invocation": {
    "argv":["pr","view","123","-R","org/repo"],
    "cwd":"/work/repo", "isTTY":false, "stdinTTY":false
  },
  "classification":{"level":"L1","confidence":"exact","resolution":"complete","mutationFree":true},
  "operations":[{
    "family":"pr.view", "action":"pr.view", "effect":"read",
    "target":{"host":"github.com","owner":"org","repository":"org/repo","number":123},
    "execution":"will-run", "origin":{"command":["pr","view"]}
  }],
  "policy":{"mode":"observe","decision":"allow"},
  "result":{
    "exitCode":42, "durationMs":18,
    "stdout":{"base64":"b2sK","size":3,"truncated":false},
    "stderr":{"base64":"","size":0,"truncated":false}
  }
}
```

A signaled result instead has exitCode=-1 and signal, e.g. `SIGTERM`. A
noncapturing hook has no stdout/stderr. `hooks test --phase post` supplies a
**synthetic** exitCode=0 result without running gh; it cannot verify a real
post-execution result. Exit zero with `{"decision":"abstain"}` is suitable for
an audit hook.

## Decisions versus outcomes and onError

| Outcome | Cause | Pre effective decision |
| --- | --- | --- |
| ok | Exit zero, valid response or empty stdout | Hook decision. |
| error | Spawn failure, nonzero exit, wait/copy error | onError. |
| timeout | Hook deadline expires | onError. |
| canceled | Parent signal/context or earlier pre denial | onError; cannot undo existing denial. |
| invalid-response | Protocol/overflow error | onError. |
| skipped | Optional unavailable command, disabled/phase mismatch, missing/failed dependency, defensive cycle/duplicate | abstain. |

Pre onError: deny -> deny (default), allow -> allow, ignore -> abstain.
Post effective decision and aggregate are **always abstain**, even if its valid
response says deny. Post onError defaults ignore; config rejects post onError=deny.
A timeout handler exiting zero still has timeout outcome. ExitCode is diagnostic,
not a shortcut for determining outcome.

Deny dominates all allows. Enforce aggregates policy and pre-hook denial before
starting gh, returning 1 with a redacted diagnostic. Observe runs hooks and logs
would-deny but executes gh; denial cancellation still short-circuits that hook
batch. Normal real-gh exits/signals are reproduced after post-hooks. Management
usage errors are 2; `hooks test` failures/denials are 1; explain displays policy
without hook execution and exits zero even on displayed deny.

## Selection, ordering, dependencies, concurrency

Hook phase is configured `pre`/`post`, not a match property. Match criteria are
AND; entries within each list are OR; operation/effect/repository/host criteria
must hold on the same operation. Match fields: operation action globs, command
path prefixes, repositories, hosts, effect, level. Dots are ordinary glob
characters: `pr.merge.*` includes multi-segment descendants; `pr.merge` is exact.
Repository/host globs are case-insensitive; repository `org/*` does not span `/`.
Command prefixes are token-delimited, using alias-expanded argv and canonical
native origin after global flags. Skipped/unselected operations are ignored.
Unresolved repository/host constraints are selected conservatively.

`env: {NAME: "value-glob"}` applies to rules and both hook phases. It reads the
caller's invocation environment, not hook-specific `env` overrides. Names must
match `[A-Za-z_][A-Za-z0-9_]*`; values use case-sensitive `path.Match` syntax.
`"*"` means set and non-empty, including values containing `/`; `""` means
unset or empty. All map entries must hold, ANDed with the other criteria.
Environment is caller-controlled: a guardrail against mistakes, **not a
security boundary**.

```yaml
policy:
  mode: enforce
  rules:
    - id: no-worker-merges
      match: {env: {MALL_GH_WORKER: "1"}, operation: "pr.merge.*"}
      decision: deny
```

`token: app | user | unknown | none` matches the kind of credential in
`GH_TOKEN`/`GITHUB_TOKEN` (prefix only; the value is never logged). Wrappers such
as gain call `gh` through PATH with a GitHub App token, so they pass through
ghok and can be told apart:

```yaml
policy:
  rules:
    - id: no-app-pr-writes
      match: {token: app, operation: ["pr.*", "issue.*"], effect: write}
      decision: deny
      reason: PR/issue writes must be authored by you, not the GitHub App
```

Post hooks may also filter on how the real gh finished (rules and pre hooks
reject these, since gh has not run yet):

- `result: success | failure` — success is exit status 0; failure is a non-zero
  exit, termination by a signal, or gh failing to start (`startError`).
- `exitCode: [n, ...]` — gh exit statuses 0–255 (never matches a signal
  termination or a start failure).

```yaml
- id: notify-on-gh-failure
  phase: post
  match: {command: "pr merge", result: failure}
  command: ["./hooks/notify"]
```

Post-hook candidates are selected before gh runs (which decides supervision and
output capture) and filtered by these criteria once the result is known; hooks
that do not match the result are not started. `ghok hooks test --phase post
--exit-code N -- <gh args>` exercises this selection with a synthetic result.

`ghok hooks test` reports the hook aggregate (`aggregate.decision`: `allow`, `deny`,
or `abstain`), policy decision (`policy.decision`: `allow`, `audit`, or `deny`),
and FINAL decision (`final.decision`) separately. No matching hooks or all abstaining
gives hook aggregate `abstain`; some allow gives `allow` unless a deny dominates.
Post aggregate remains abstain. Policy's decision already reflects configured
mode: observe policy denial is `audit` with `policy.wouldDeny: true`.
FINAL denies in enforce if policy or pre-hooks deny; observe instead gives
`audit` and `final.wouldDeny: true`.

`--expect allow|deny|abstain|audit` preserves its previous comparison: **FINAL**,
now printed as FINAL and serialized as `final.decision`, not the raw hook aggregate
or wouldDeny. Policy allow plus hook abstention yields FINAL allow, so
`--expect abstain` does not match it. Use `--expect-hooks allow|deny|abstain`
to assert hook abstention/allow/denial and `--expect-policy allow|audit|deny`
to assert policy independently. With any expectations, all must match for exit 0,
even on denial/hook execution error; mismatches exit 1 and name every failed
expectation with expected/actual values. Without expectations, FINAL deny or
hook execution error (nonzero exit/spawn failure, timeout, invalid response,
cancellation) exits 1, otherwise 0. Configuration/analysis failures exit 1;
usage errors exit 2.

- Lower priority runs first; equal priority follows config order. Dependencies
  are ordered before dependents; among ready nodes priority/config order wins.
- runtime.hookConcurrency defaults 1. Increasing it permits independent ready
  hooks to overlap; completion order is not result order.
- dependsOn lists hook IDs in the same phase. Config rejects unknown IDs,
  cycles, duplicates, and cross-phase dependencies. A dependency that is not
  selected leaves its dependent skipped rather than forcing its match.
- A non-optional dependent skips after a non-ok dependency; an optional
  dependent may receive failed dependency records. Direct dependencies are
  delivered in previous, in dependsOn order. Denied pre dependencies still
  trigger global short-circuit, including optional dependents.
- Any effective pre deny cancels pending/running hooks. Returned diagnostics
  include canceled hooks, in selection order. First effective deny in that
  order supplies message, then reason, then error; not the fastest completion.
- Cancellation sends TERM to each hook group, waits 500 ms, sends KILL even if
  the leader already exited, and waits the direct child. Retained inherited
  pipes after normal exit cause bounded wait error and group KILL. Descendants
  escaping the group are unsupported.
- SIGINT/SIGTERM/SIGHUP cancel resolver/pre-hook helper groups and re-raise the
  signal after cleanup, without starting the user's gh command. The watcher
  excludes startup-ignored signals and stops/joins before real-gh supervision.
  During post-hooks it restarts, cancels/reaps hooks, logs the interruption, and
  preserves gh's already completed exit code/signal. SIGKILL cannot be handled:
  hooks may then outlive ghok.

## Environment and execution context

The base environment is inherited, then hook env overrides apply. ghok metadata
overrides both:

| Variable | Value |
| --- | --- |
| GHOK_EVENT | pre/post |
| GHOK_EVENT_ID | Input.eventId |
| GHOK_HOOK_ID | Configured ID |
| GHOK_NESTED | 1; bypass recursive wrapper analysis/hooks |
| GHOK_REAL_GH | Discovered real path when available |
| GHOK_DEPTH | Inherited current wrapper activation depth; nested wrapper increments it |

Hooks inherit invocation cwd, not the config directory. Executable resolution
against the config directory does not rewrite arguments or cwd. Bare executable
names use effective hook PATH; config env values are not credential-filtered.
There is no interactive TTY stdin; it contains protocol JSON only.

## Configuration and resource limits

| Setting | Default / constraint |
| --- | --- |
| id, phase, command | Required; unique ID, valid phase, nonempty argv. |
| enabled | true; disabled excluded from selection. |
| priority | 0; ascending/config-order ties. |
| dependsOn | none; validated same-phase DAG. |
| optional | false; true skips missing/non-executable command and permits failed dependencies. |
| timeout | 10s, maximum 10m; positive duration. |
| onError | pre deny; post ignore. |
| maxOutputBytes | Config default 1 MiB **for each stdout and stderr stream**. |
| captureOutput | false; post only. |
| redactInput | false; set true for hooks forwarding input elsewhere. Redacts only that hook's input and reports substitution counts; does not change inherited credentials. |
| env | none; overrides inherited values except ghok metadata above. |
| runtime.hookConcurrency | 1, positive. |
| runtime.captureLimit | 1 MiB retained per gh stdout/stderr stream when capture enabled. |
| runtime.stdinMaxBytes / fileMaxBytes | 8 MiB analysis bounds. |
| runtime.stdinTimeout | 30s. |
| runtime.resolveTimeout | 5s for context API calls. |

The bare Runner API, when MaxOutputBytes <=0, defensively uses stdout 1 MiB and
stderr 64 KiB. **Loaded CLI config supplies a positive default 1 MiB to both**;
the 64 KiB fallback is not the normal configured stderr limit. Excess output is
drained (not retained) to avoid pipe deadlock; stdout overflow invalidates the
response, stderr overflow appends a truncation marker.

The complete YAML example is [examples/config.yaml](../examples/config.yaml).
For hooks test and installation commands see [README](../README.md). Detailed
implementation evidence: [hooks/policy notes](notes/hooks-policy.md),
[configuration notes](notes/config-logging-redaction.md), and
[process notes](notes/process.md).
