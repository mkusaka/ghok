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

Contract: `internal/protocol/protocol.go` and `internal/model/`, published as
[`hook-input.schema.json`](../schema/hook-input.schema.json) and
[`hook-response.schema.json`](../schema/hook-response.schema.json). Print them with
`ghok schema hook-input` / `ghok schema hook-response`; regenerate all schemas
with `go generate ./internal/schema`. Enum/doc/const tags in Go are authoritative.

Non-optional fields are serialized even for zero values. Optional fields use
omitempty and may be absent. All typed arrays are non-nullable: empty slices are
omitted, and **absent arrays mean empty**. This includes `operations` and
`invocation.argv`; arbitrary values inside `parameters` may still be JSON null.
Integers are JSON numbers; captured bytes are base64, never assumed UTF-8.

`schemaVersion` has JSON Schema `const: 1`. Additive fields never bump the version;
renames and removals bump it and are breaking changes. Consumers should tolerate
new fields. Generated schemas describe the fields known to this build, not a
runtime input-validation step.

### Top-level Input

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| schemaVersion | integer | no | Always 1, set by runner. |
| event | string | no | `pre`, `post`, or `transform`, set by runner. |
| phase | string | yes | `pre` for transform input. |
| eventId | string | no | Invocation correlation ID, shared across hooks/phases. |
| hookId | string | no | Current configured hook ID. |
| redactions | integer | no | Credential substitutions made in this hook's input; zero for raw input. Counts repeated representations separately, not unique secrets. |
| invocation | InvocationInfo object | no | Original/expanded argv and execution context. |
| classification | Classification object | no | Invocation-level normalization identity/resolution/proof. |
| operations | Operation array | yes | All normalized operations, including execution evidence; absent means empty. |
| errors | string array | yes | Nonfatal normalization problems; raw unless redactInput is enabled. |
| context | ResolvedContext object | yes | Resolver context when requested. |
| policy | PolicyInfo object | yes | CLI policy evaluation, not aggregate pre-hook decision. |
| result | ExecResult object | yes | Real gh outcome in post phase; absent before execution. |
| previous | HookResult array | yes | Direct dependsOn results in configured dependency order. |

### InvocationInfo

| Field | Type | Optional | Meaning |
| --- | --- | --- | --- |
| via | string | no | `shim` for the gh shim, `exec` for `ghok exec -- <gh args>`; identical enforcement pipeline. |
| argv | string array | yes | Original arguments after argv[0]; absent means empty; raw unless redactInput is enabled. |
| alias | string | yes | First expanded plain alias name. |
| expanded | string array | yes | Alias-expanded analysis argv; raw unless redactInput is enabled. Real gh receives original argv unless a trusted transform changes the request. |
| cwd | string | no | Invocation working directory. Hook inherits this cwd. |
| isTTY | boolean | no | Original stdout is a terminal. Capture can make child stdout non-TTY. |
| stdinTTY | boolean | no | Original stdin is a terminal. |
| realGh | string | yes | Discovered real executable path. |
| tokenKind | string | yes | Kind of env token gh will use: `app` (`ghs_`, GitHub App installation, e.g. gain or Actions), `user` (`ghp_`/`gho_`/`ghu_`/`github_pat_`), `unknown`, or `none` (gh's stored login). The token itself is never included. |
| envChanged | string array | yes | Sorted set/unset names changed by transforms; values are never included. |

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

Target fields are all optional: kind, host, owner, repository (`owner/name`), nodeId,
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
| warning | string | yes | Non-blocking text from a successful allow/abstain response; ignored for deny, including continued deny. |
| effective | string | yes | Decision used after phase/onError/continueOnError handling. |
| continuedOnError | boolean | yes | True when continueOnError changed a pre deny to effective abstain; outcome/decision remain unchanged. |
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
| schemaVersion | integer | no | Omit or use 1 (`const: 1`); the parser also tolerates explicit 0, outside the published schema. Other versions are invalid. |
| decision | string | yes for nonempty response | Only `allow`, `deny`, `abstain`; policy-only `audit` is not a hook decision. |
| reason | string | no | Recorded explanation; fallback denial diagnostic. |
| message | string | no | User-facing denial message on stderr. |
| warning | string | no | Non-blocking text for successful allow/abstain only; ignored for deny. |

Exit zero with empty/whitespace stdout yields ok/abstain. Nonzero exit is error
regardless of stdout. Unknown JSON object keys are currently ignored, not
strictly rejected. Multiple documents, trailing garbage, invalid JSON/types,
unknown decisions/versions, and stdout overflow produce invalid-response.
Overflow invalidates even an otherwise valid retained JSON prefix: unseen bytes
cannot establish a valid single document. Stderr alone is not a failure.

Warnings never create a `warn` hook decision. They are credential-redacted at
display/log boundaries. When gh runs, pre-hook warnings print in selection
order after rule and transform warnings; post-hook warnings print in selection
order after gh output. Enforced denial suppresses execution-only warning output.
Terminal warnings use the same sanitizer and per-entry 40-line/4-KiB/
500-character bounds as denials; JSON and logs retain structured values without
terminal escaping/truncation. `hooks test` text/JSON exposes warnings but does
not emit execution-only stderr warnings. Its expectation and exit rules are
unchanged. Policy warn rules are pre-only allow-plus-warning, with `reason` as
text; unresolved repository/host criteria still match conservatively under the
default `onUnresolved: deny`. They cannot override another denial.

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
    "via": "shim",
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
    "via":"shim", "argv":["pr","view","123","-R","org/repo"],
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
**synthetic** exitCode=0 result (or `--exit-code N`) without running gh; it cannot
verify a real post-execution result. Exit zero with `{"decision":"abstain"}` is suitable for
an audit hook.

`ghok hooks test --print-input [--phase pre|post] [--hook ID] -- <gh args>`
previews this JSON envelope without executing hooks or gh. Post includes the
synthetic result. `--hook ID` selects a matching configured hook, sets `hookId`
and honors `redactInput`; without it `hookId` is empty. No dependency hooks run,
so `previous` is absent. `invocation.via` is `shim`, representing the wrapper
input being previewed.

Display is credential-masked by default with explain's redactor. Display-only
masking preserves the payload's `redactions` counters and emits
`N values masked; use --show-secrets for the bytes the hook receives` on stderr
when substitutions occur. `--show-secrets` prints the actual hook payload,
including configured `redactInput` masking; it never overrides that setting.
`--allow-api` cannot be combined with `--print-input`, keeping preview free of
gh execution.

## Decisions versus outcomes and onError

| Outcome | Cause | Pre effective decision |
| --- | --- | --- |
| ok | Exit zero, valid response or empty stdout | Hook decision. |
| error | Spawn failure, nonzero exit, wait/copy error | onError. |
| timeout | Hook deadline expires | onError. |
| canceled | Parent signal/context | onError. |
| invalid-response | Protocol/overflow error | onError. |
| skipped | After pre-hook denial, optional unavailable command, disabled/phase mismatch, missing/failed dependency, defensive cycle/duplicate | abstain. |

Pre onError: deny -> deny (default), allow -> allow, ignore -> abstain.
Post effective decision and aggregate are **always abstain**, even if its valid
response says deny. Post onError defaults ignore; config rejects post onError=deny.
A timeout handler exiting zero still has timeout outcome. ExitCode is diagnostic,
not a shortcut for determining outcome.

Pre hooks may set `continueOnError: true`. `onError` still classifies failures,
then any resulting deny (returned by the hook or produced by `onError: deny`)
becomes **effective abstain**, never allow. `outcome`, `decision` and diagnostics
retain what happened; `continuedOnError: true` records the override in results,
logs, `hooks test` text/JSON and dependents' `previous`. Such dependencies count
as satisfied even when the real outcome is non-ok. No deny means no override
or continued flag. Post hooks reject the option because they never block;
transforms reject it with guidance to use `onError: allow`.

When gh will run (enforce allow or observe), each continued denial prints:

```text
ghok: warning from hook check (continue-on-error)
  Review required.
```

The warning uses message, else reason, else error. Error outcomes include the
last two non-empty stderr lines, capped at 200 bytes each; stderr is withheld
for `redactInput` hooks. Credential redaction and the denial terminal sanitizer
apply, with 40-line/4-KiB/500-character limits for the entire warning. If another
denial prevents gh from running, continued results are still logged, but no warning is printed.
`hooks list` shows `continueOnError`; `hooks test` never runs gh or prints these
execution-only warnings. Its execution-error exit status remains diagnostic.
Continued diagnostics also enter the log's `warnings` list, so decision-level
logging retains allowed invocations with these warnings.

Deny dominates all allows. Enforce aggregates policy and pre-hook denial before
starting gh: content denials exit **77** (`EX_NOPERM`), hook-`onError`-only denials
exit **78** (`EX_CONFIG`). Mixed content/error denial exits 77. Invalid config
that fails closed, or a matching inactive gate, exits 78 when no content denial
exists. ghok internal errors remain 1; missing real gh is 127.
Observe logs would-deny but executes gh unchanged; effective denial still skips
pending hooks, while already-running hooks finish within their own timeout.
Normal real-gh exits/signals are reproduced after post-hooks. Management usage errors are 2; `hooks test` failures,
denials and expectation mismatches remain **1**, not 77/78. Explain displays
policy without hook execution and exits zero even on displayed deny.

### Stable stderr denial contract

The first line has this stable machine-readable shape:

```text
ghok: denied by <source>[ (error: <kind>)][ (+N more)]
```

Sources: `transform <id>`, `hook <id>`, `rule <id>`, `policy unknownMutation`,
`policy protectedUnknown`, `policy protectedRepositories`, `policy unknownInput`,
`config <id>` for an inactive gate, or `config` for invalid configuration.
Hook `onError` denials alone have an error kind: `timeout`,
`exit <code>`, `invalid response`, `spawn failed`, or `canceled`.
`exit -1` means a started hook was terminated by a signal; its entry includes
the signal error. `spawn failed` means the hook never started.
The optional `+N more` counts the remaining denial entries.

The first entry is selected in this deterministic order, also used for all
following entries: rules in config order, policy handling, inactive gates, hooks that
**returned** deny in selection order, then hook `onError` denials in selection
order. Parallel completion order never determines the first source.

Each entry is `  - <source>: <first line of message, or reason if no message>`.
Further message lines use four-space indentation. Message wins over reason;
equal reason/message appears once. They are never joined with `"; "`.
All display values are credential-redacted as before.

One shared terminal sanitizer applies to denial entries, `hooks test` text,
explain, and doctor/validate and hook-list/trust notes. It preserves `\n` and
`\t`, but displays all other C0/C1 controls and DEL as visible `\xNN` escapes
(for example ESC → `\x1b`, CR → `\x0d`). Invalid UTF-8 bytes are escaped the same
way. A denial entry, including its prefix, indentation, markers and final
newline, is at most 40 lines and 4 KiB; each line is at most 500 Unicode
characters after escaping. Overlong lines end with `…`; line/byte overflow
omits remaining lines with an indented `… (N more lines truncated)` marker.
That marker counts toward the 40-line limit. Other text diagnostics use the
same per-entry bounds; reports are not truncated as a whole.
Sanitization still applies with `--show-secrets`. JSON (`--json`, `--print-input`)
and JSONL logs are not terminal-sanitized or display-truncated: their existing
structured/redacted policy and hook results retain both reason and message.

```text
ghok: denied by rule no-admin (+1 more)
  - rule no-admin: Use the normal merge workflow. (pr.merge.direct)
  - hook check: hook timed out
```

The example exits 77. With only the timed-out hook, the header is
`ghok: denied by hook check (error: timeout)` and the exit code is 78.

`ghok exec -- <gh args>` runs the shim's entire pipeline once, without requiring
a shim on PATH; it is not a policy bypass. Discovery, depth/token checks,
hook input and results, logging, real-gh status and signals are the same.
Only `invocation.via` changes to `exec`, also recorded in JSONL logs.

## Selection, ordering, dependencies, concurrency

Hook phase is configured `pre`/`post`, not a match property. Match criteria are
AND; entries within each list are OR; operation/effect/repository/host criteria
must hold on the same operation. Match fields: operation action globs, command
path prefixes, repositories, hosts, effect, level, parameters. Dots are ordinary glob
characters: `pr.merge.*` includes multi-segment descendants; `pr.merge` is exact.
Repository/host globs are case-insensitive; repository `org/*` does not span `/`.
Command prefixes are token-delimited, using alias-expanded argv and canonical
native origin after global flags. Skipped/unselected operations are ignored.
Unresolved repository/host constraints are selected conservatively. Command
and REST operations gh runs without a repository (`/rate_limit`, gists) never
match `repositories` and do not borrow the current repository; GraphQL documents
and unclassified operations still do.

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

`parameters: {name: "value-glob"}` matches normalized operation parameters (keys
as in [normalization.md](normalization.md), e.g. `watch` for `pr checks
--watch`) on the same operation as the other per-operation criteria. Scalars
compare by their text (`true`, `42`); globs, `"*"` and `""` work as for `env`.
Arrays and objects only satisfy `"*"`.

`unless` is a list of matches next to `match` on rules, hooks and transforms.
An operation that any entry matches definitively is excluded from `match`;
a maybe-match (unresolved repository/host, or post-hook `result`/`exitCode`
before gh has finished) never excludes, so selection stays conservative. An entry without per-operation criteria (only `command`, `env`,
`token`, `level`, `result`, `exitCode`) excludes the whole invocation. Empty
entries are rejected.

```yaml
transforms:
  pre:
    - id: gain-token-read
      match: {effect: read, env: {GH_TOKEN: ""}}
      unless:
        - operation: run.watch            # long-running: App tokens expire
        - operation: pr.checks
          parameters: {watch: "true"}
      command: ["./transforms/gain-token"]
```

Post hooks may also filter on how the real gh finished (rules and pre hooks
reject these, since gh has not run yet):

- `result: success | failure` — success is exit status 0; failure is a non-zero
  exit, termination by a signal, or gh failing to start (`startError`).
- `exitCode: [n, ...]` — gh exit statuses 0–255 (never matches a signal
  termination or a start failure).

```yaml
hooks:
  post:
    - id: notify-on-gh-failure
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

### Parallel hooks
<!-- desc: Run hooks in parallel, join background work, and order dependencies. -->

- Hooks are written as `hooks.pre` and `hooks.post` lists; the list sets the
  phase. Each runs in order, user config hooks before project hooks. A hook
  starts after every earlier selected non-background hook finished and after the
  background hooks in its wait list (GitHub Actions semantics). `background: true`
  hooks do not block later hooks. `- wait: id|[ids]` and `- wait-all:` list items
  add earlier background hooks to the wait list of every later hook; a
  `- parallel: [hooks]` block makes its members background hooks and waits for all
  of them after the block. Project hooks also wait for every user background
  hook. Hooks that do not match are absent and never waited for. A phase finishes
  only after all of its hooks finished.
- runtime.hookConcurrency (default 4) caps running hooks; excess hooks queue.
  Completion order is not result order.
- A failed or abstaining hook does not stop later hooks unless `onError` makes
  it an effective deny. Ordering guarantees between hooks come from waits and
  dependsOn only; use dependsOn to run a hook only after a gate allowed.
- dependsOn lists earlier hook IDs in the same phase. Config rejects unknown
  IDs, later hooks, duplicates, and cross-phase dependencies. A dependency
  inside a parallel block serialises that pair. A dependency that is not
  selected leaves its dependent skipped rather than forcing its match.
- A non-optional dependent skips after a non-ok dependency, except when that
  result has `continuedOnError: true`; an optional dependent may receive failed
  dependency records. Direct dependencies are delivered in previous, in
  dependsOn order, with their real outcome and decision. Effective pre denials
  still skip pending hooks, including optional dependents.
- Any effective pre deny skips hooks not yet started with outcome `skipped`
  and reason `after pre-hook denial`. Already-running parallel siblings and
  background hooks finish within their own timeout; denial does not cancel
  them. The phase waits for them before reporting its aggregate. Returned
  diagnostics remain in selection order; terminal denial entries put content
  denials before hook-error denials as specified above.
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
| GHOK_EVENT_SOURCE | Entry point: `shim`, `exec` or `test` (`ghok hooks test`). Use it **only** to skip side effects such as Slack notifications during tests — never to change the decision; the input JSON is identical in every source so tests exercise the real path. |
| GHOK_HOOK_ID | Configured ID |
| GHOK_NESTED | 1; recursive passthrough only with a valid GHOK_NESTED_TOKEN |
| GHOK_NESTED_TOKEN | Random invocation token, verified by a private hash-named file under the user config directory's state/nested/; hook env cannot override it |
| GHOK_REAL_GH | Discovered real path when available |
| GHOK_DEPTH | Inherited current wrapper activation depth; nested wrapper increments it |

The CLI keeps the token file until resolver/hooks finish and removes it before
exec/exit. The original gh command does not receive the newly issued token.
`GHOK_NESTED=1` alone runs normal analysis/policy/hooks. See [security](security.md)
for file permissions, cleanup limits, and the non-security-boundary model.

Hooks inherit invocation cwd, not the config directory. Executable resolution
against the config directory does not rewrite arguments or cwd. Bare executable
names use effective hook PATH; config env values are not credential-filtered.
There is no interactive TTY stdin; it contains protocol JSON only.

## Configuration and resource limits

| Setting | Default / constraint |
| --- | --- |
| id, command | Required; unique ID and nonempty argv. The containing `hooks.pre` / `hooks.post` list sets phase; there is no per-hook `phase` key. |
| enabled | true; disabled excluded from selection. |
| background | false; true starts without blocking later hooks (parallel block members are background). |
| dependsOn | none; earlier hooks of the same phase. |
| optional | false; true skips missing/non-executable command and permits failed dependencies. |
| timeout | 10s, maximum 10m; positive duration. |
| onError | pre deny; post ignore. |
| maxOutputBytes | Config default 1 MiB **for each stdout and stderr stream**. |
| captureOutput | false; post only. |
| redactInput | false; set true for hooks forwarding input elsewhere. Redacts only that hook's input and reports substitution counts; does not change inherited credentials. |
| env | none; overrides inherited values except ghok metadata above. |
| runtime.hookConcurrency | 4, positive; caps hooks running at once. |
| runtime.captureLimit | 1 MiB retained per gh stdout/stderr stream when capture enabled. |
| runtime.stdinMaxBytes / fileMaxBytes | 8 MiB analysis bounds. |
| runtime.stdinTimeout | 3s. |
| runtime.resolveTimeout | 2s for context API calls. |
| runtime.resolveAPI | auto: implicit read-only API only for actionable repository/host matches, in observe or enforce; never: no implicit API. Logging alone does not resolve. |

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

## Request transforms

Transforms are trusted executables, not hook decisions. Configure `transforms.pre`
as a list; user entries run first, then trusted project entries, serially. Each is
matched against the current request after the preceding successful edit. A
repository/host criterion selects a transform only on a definite match: implicit
targets are filled from env and local git (no API), and anything still unresolved
leaves the request unchanged. IDs share
one namespace with pre/post hooks. Supported keys are `id`, `match`, `unless`, `command`,
`env`, `timeout` (default `5s`, maximum `10m`; token minting may need `15s`), `onError` (`deny`, default, or `allow`),
`redactInput`, and project-only `files`. Scheduling/dependency keys (`parallel`,
`background`, `wait`, `wait-all`, `dependsOn`), `optional`, and `captureOutput`
are not supported. Any `transforms.post` value is a configuration error:
`transforms.post is reserved`.

Input is the hook Input envelope with `event: "transform"` and `phase: "pre"`,
current operations and invocation, and no `previous`. Commands inherit the same
environment/cwd, nested-call token, timeout/cancellation and redaction rules as
hooks. Write diagnostics to stderr. Empty stdout means unchanged; otherwise
return one schema-version-1 JSON object:

```json
{"schemaVersion":1,"set":{"/operations/0/texts/body/value":"new body"},"unset":[]}
```

An optional string `warning` reports a non-blocking advisory, with or without
edits, only for a successful transform. For example:
`{"schemaVersion":1,"warning":"Check the release window."}`.
Failed transforms do not contribute warnings. Wrapper output orders rule
warnings, transform-result warnings, then pre-hook warnings, only when gh runs;
management explain/hooks-test reports them as diagnostics without running gh.
Warnings are redacted and terminal-sanitized like hook warnings.

### Transform environment edits

Use a trusted transform such as `gain-token` to set `GH_TOKEN` before policy and hooks.

The optional response `env` is a flat object of environment names to strings
(set, including an empty string) or `null` (unset), for example
`{"schemaVersion":1,"env":{"GH_TOKEN":"<minted token>","GITHUB_TOKEN":null}}`.
Omit `env` for no environment edit; `env: null` is invalid. Names must match
`^[A-Za-z_][A-Za-z0-9_]*$`; string values must not contain NUL.
Environment edits have three tiers:

* Forbidden: `GHOK_*`, `GH_PATH`, `GH_CONFIG_DIR`, `XDG_CONFIG_HOME`, `HOME`,
  `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `DYLD_*`, and reserved targeting
  variables `GH_HOST` and `GH_REPO`.
* Credentials: `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN` and
  `GITHUB_ENTERPRISE_TOKEN` may be set/unset; token kind is recomputed with the
  existing github.com precedence (`GH_TOKEN`, then `GITHUB_TOKEN`); enterprise
  tokens are not currently classified.
* Other valid names may be freely set/unset.

Invalid environment edits are `invalid-response`. Request and environment
edits apply atomically; later entries win for the same variable. Each transform's
selection and command environment use the state before that entry, so an edit
cannot re-trigger its own transform. Later transforms see prior successful edits.
Final `invocation.tokenKind`, `match.token` and `match.env` use the edited
environment, including tokenKind in PRE hook input.
gh and POST processes receive the edited environment; PRE processes retain the original environment.

`invocation.envChanged` is an optional sorted list of changed names, never values.
JSONL `transformed.env` and `transformed.envBy` contain only sorted changed
names and name-to-transform attribution, respectively. Explain/hooks-test
transformed metadata also reports unset names (`envUnset`) and final `tokenKind`;
environment values are never displayed, even with `--show-secrets`.
Every set value is registered with the shared redactor before diagnostics or
hook input, including values from non-credential variables.

### Request edits

`set` maps strict JSON pointers to replacement JSON values; `unset` lists paths
to remove. Only these paths are allowed (N is a canonical zero-based operation
index; JSON-pointer escaping applies to parameter keys):

* `/operations/N/texts/title/value`, `/operations/N/texts/body/value`,
  `/operations/N/texts/notes/value`
* `/operations/N/parameters/KEY`, where KEY has a supported editable mapping
* `/operations/N/target/repository`, `/operations/N/target/number`

Other paths, malformed pointers and invalid response shape are `invalid-response`.
An allowed path without an unambiguous writable source, an unsupported value,
or a failed semantic round trip is `unsupported-edit`. Native parameter edits
use curated one-to-one flag mappings, not arbitrary flag construction. Existing
native repository/number selectors can be rewritten. REST JSON input keys and
GraphQL variables (including `--input` variables) can be rewritten. Existing REST
`-f`/`-F` prose can be edited; typed `-F` becomes raw `-f` so text is not coerced.
Non-text REST field insertion is unsupported. GraphQL AST literals/defaults and
variables shared by selected root fields are unsupported. Generated/editor,
unavailable or truncated text cannot be safely rewritten.

Edits are atomic and must re-normalize to the same semantic projection: action,
family, effect, execution, origin and level stay unchanged; edited values must
match, and untouched text values/parameters stay unchanged. Transport provenance
may change when a shared JSON file is rewritten. Evidence, detail,
unresolved/resolution metadata and resolver-filled target fields do not establish
write-back identity. A no-op or environment-only edit preserves original execution
argv and bytes; a changed request executes expanded argv. File/stdin edits use 0600 temporary files and never mutate
original inputs. Failed attempts remove their temporary files; successful
transport is removed after gh finishes.

`onError: deny` exits 78 without gh in **both** policy modes. `onError: allow`
rolls back the entire transform chain's request and environment edits, skips its
remaining entries, then applies policy/hooks to the original request and environment. Policy and ordinary pre/post hooks see the
final request. `explain`, `hooks test`, and `hooks test --print-input` run transforms
before their evaluation/preview, but never execute the requested gh command;
`normalize` remains a pure inspection of the original request.

Project transforms use hook trust/digests, dependency files, `trust.include`,
review notes and ignored-command warnings. Inactive transforms are ignored,
never inactive gates. JSONL diagnostics record transform outcomes and redacted
edit summaries, not replacement prose; normal text minimization still applies.
