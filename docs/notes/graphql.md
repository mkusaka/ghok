# GraphQL normalization

`Normalize(model.Invocation, model.APIRequest) model.Result` is pure. File reads and captured stdin come only from the invocation; no environment, filesystem or network access occurs during normalization.

## Request semantics

Verified against cli/cli v2.102.0:

- `pkg/cmd/api/api.go`, `apiRun`: `--input` is the verbatim request body; field flags become URL query parameters, not body variables.
- `pkg/cmd/api/http.go`, `groupGraphQLVariables`: for the literal `graphql` endpoint and non-GET requests, `query` and `operationName` remain top-level; other fields become variables. Absolute URLs and non-literal relative endpoints (`/graphql`, `graphql/`) do **not** invoke this grouping. Nested `variables[...]` supplies their actual body variables.
- `pkg/cmd/api/http.go`, `httpRequest` and `addQuery`: GET fields are URL parameters, not grouped variables. We classify the document and note the transport difference, without asserting GitHub accepts GET.
- Shared `internal/normalize/apiargs` implements raw strings, typed scalars, nested keys and injected `@file`/`@-` reads. Unreadable query/operation-name input is unknown. Unreadable variables remain unresolved; an unavailable supplied value never falls back to a default.
- Host precedence: absolute URL, `--hostname`, `GH_HOST`, otherwise unresolved. Source and targets use lower-case instance hosts: `api.github.com` becomes `github.com`, and `api.<tenant>.ghe.com` becomes `<tenant>.ghe.com`. Ports 80/443 are removed; custom ports and GHES `api.` hostname prefixes are preserved. Node IDs do not imply repository names; repository resolution is deferred to the API resolver.
- `internal/ghinstance/host.go`, `RESTPrefix`: non-literal relative GraphQL endpoints use REST routing. On known GHES hosts they route to `/api/v3/graphql`, not GraphQL, so normalization returns `unknown-endpoint` with unknown effect. Public/tenancy instances retain classification without field grouping; unresolved hosts retain classification with a routing uncertainty note. Literal `graphql` retains GHES GraphQL routing and field grouping.

JSON bodies must be objects with a nonempty string query, optional string operation name, and optional object variables. Batched arrays are unsupported. File errors lack a typed size-limit sentinel in the shared contract: recognizable size-limit messages map to `input-too-large`; other read failures map to `input-unavailable`.

## AST and bounds

Uses gqlparser/v2 v2.5.62 `parser.ParseQuery`, never regex extraction. The same library's iterative lexer preflights nesting, ignoring brackets inside strings/comments. Documents and captured bodies/files are capped at 1 MiB; syntactic/fragment depth at 256; expanded selections at 10,000. Limits return explicit unknown, never a mutation-free proof. Source retains at most 64 KiB of the document and no request variable values or argv; literals/defaults embedded in the document remain document text and require downstream redaction.

Selected root mutation fields are expanded through fragments in document order. Aliases never change identity. Non-selected mutations remain visible with `not-selected` execution and evidence. Literal/resolved-boolean skip/include directives can prove skipping; unavailable or nonboolean conditions mean `maybe`. Unknown directives are noted and ignored for execution, as required by the normalizer contract. Nested result fields are not mutations.

Queries emit one read operation, including introspection. Subscriptions are unknown because GitHub does not support them. Mutation-free is proven only for a selected query or a mutation whose root fields are all proven skipped. Missing/cyclic fragments, incompatible root fragment types, duplicate operation/fragment names, duplicate resolved arguments/input fields, missing/ambiguous operation selection and parser errors return unknown. This is syntactic normalization, not full GitHub schema/type/authorization validation; server acceptance is not promised.

## Mutation schema snapshot

`schema_mutations.go` embeds all 278 mutation field names from the [public GitHub schema](https://docs.github.com/public/fpt/schema.docs.graphql), fetched on 2026-10-14. Regenerate with `go generate ./internal/normalize/graphql`; the generator is not used at runtime. Offline generation: `go run ./internal/normalize/graphql/generate -schema /path/to/schema.graphql -date YYYY-MM-DD -output internal/normalize/graphql/schema_mutations.go`.

The detailed registry covers PR merge/auto-merge/queue, reviews, create/edit/close/reopen/draft/ready/revert/update-branch, refs, branch merging, issues/comments, repository create/edit/archive, branch protection and stars. All original input values remain in Parameters; merge methods also get lowercase `method`, and commit headline/body presence is recorded separately. `mergeBranch` is `branch.merge`, never `pr.merge`. Absent merge method uses the schema's MERGE default; an unavailable provided method never uses that default.

Schema details: `DequeuePullRequestInput.id` identifies the PR; `SubmitPullRequestReviewInput` supports `pullRequestId` or `pullRequestReviewId`; ref updates use `refId`. `deleteRepository` does not exist in this public snapshot and remains an unknown write. Schema-known unmapped fields and snapshot-absent fields both remain explicit unknown writes with exact AST identity and distinct details.

## Platform behavior

No OS-specific implementation. Injected file/stdin handling, GraphQL parsing and JSON decoding have identical semantics on macOS and Linux. Tests use synthetic invocations; no live credentials, user configuration, PATH modifications or network requests.
