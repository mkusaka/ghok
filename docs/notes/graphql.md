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

`schema_mutations.go` embeds all **278** mutations from the [public GitHub schema](https://docs.github.com/public/fpt/schema.docs.graphql), fetched on **2026-10-15**, pinned by raw-content SHA256 **`3ed095654b22b1a8e7a88af36af2b28cb6d9613da332e9c57303e30476958d42`**. It contains 1,161 input fields and 350 scalar ID candidates. Each mutation records its input type, all field type signatures (named/list/non-null), candidate ID names and name-inferred object kinds, primary ID, description first line and deprecation. Empty kind means unknown; `subjectId` does not identify one concrete kind. Bare `id` joins scalar `ID`/`ID!` fields ending in `Id`; list IDs are parameters, not singular targets.

Regenerate the current pin from the repository root:

```sh
go generate ./internal/normalize/graphql
```

The directive verifies the downloaded content against the pinned SHA before parsing or replacing the snapshot. Offline reproduction with a saved copy of that exact schema:

```sh
go run ./internal/normalize/graphql/generate \
  -schema /path/to/schema.graphql \
  -date 2026-10-15 \
  -sha256 3ed095654b22b1a8e7a88af36af2b28cb6d9613da332e9c57303e30476958d42 \
  -output internal/normalize/graphql/schema_mutations.go
```

To intentionally advance upstream, run `go run ./internal/normalize/graphql/generate -date YYYY-MM-DD -output internal/normalize/graphql/schema_mutations.go` without `-sha256` (or with `-schema /path/to/new-schema.graphql` for a saved snapshot). Use the actual fetch date. Review the emitted hash and metadata, then update the date/hash in `registry.go`'s `go:generate` directive and these docs. A changed upstream file makes the old pinned command fail rather than silently update classifications. Generation is development-only; normal builds/tests use embedded data and never fetch the schema. The generator reuses the existing gqlparser dependency and adds no dependencies.

Generation chooses the unique longest target noun matching the mutation field name, otherwise the sole candidate. It never chooses by available input values. Multiple ambiguous candidates leave `target.nodeId` empty with their names in `unresolved`; a selected but missing/nonstring/null ID is also unresolved. Repository lookup always stays deferred with source `api`.

The **239** schema-known mutations without curated overrides now use family `graphql.mutation`, action `graphql.<name>`, write effect, exact AST/schema identity and no unknown reason. For example `addReaction` extracts `subjectId`; `transferIssue` prefers `issueId` over `repositoryId`. Previously these fields were unknown-family writes with `unknown-mutation`, blocked by `policy.unknownMutation: deny`. That setting alone no longer blocks schema-known writes; use `operation: "graphql.*"` rules, optionally constrained to write effect. Curated action names are outside that namespace and retain their existing policy matches.

The **39** curated entries in `registry.go` cover PR merge/auto-merge/queue, reviews, create/edit/close/reopen/draft/ready/revert/update-branch, refs, branch merging, issues/comments, repository create/edit/archive, branch protection and stars. Existing family/action names and canonicalisation remain stable. All original input values remain in Parameters; merge methods also get lowercase `method`, and commit headline/body presence is recorded separately. `mergeBranch` is `branch.merge`, never `pr.merge`. Absent merge method uses the schema's MERGE default; an unavailable provided method never uses that default.

Curate overrides only in `mutationRegistry` and the existing canonicalisation functions; never hand-edit generated metadata. An empty `nodeID` selector uses the generated primary ID. Explicit selectors remain only for `createIssue` and `createPullRequest` (`repositoryId`, because generated candidates are ambiguous) and `submitPullRequestReview` (`pullRequestId` first, with the existing `pullRequestReviewId` fallback). `createRepository` deliberately has no target node: its owner/team IDs do not identify the new repository. `DequeuePullRequestInput.id` and ref updates' `refId` now come from generated metadata.

When adding a curated entry, preserve every existing identity, add the expected target to `TestCuratedMutationSchemaTargets`, and cover any new canonicalisation in `graphql_test.go`. The consistency test asserts every curated mutation and ID field exists in the generated input type, checks scalar-ID type, and exercises target selection with competing IDs so regeneration cannot silently redirect a curated action. Run `go test -race ./internal/normalize/graphql/...` after regeneration.

Snapshot-absent fields still use family `unknown`, action `graphql.mutation.<name>`, write effect and `unknown-mutation`, even when aliased to a known field. `deleteRepository` is absent from this public snapshot. Descriptions/deprecation are explanatory metadata, not full schema/type/authorization validation.

## Platform behavior

No OS-specific implementation. Injected file/stdin handling, GraphQL parsing and JSON decoding have identical semantics on macOS and Linux. Tests use synthetic invocations; no live credentials, user configuration, PATH modifications or network requests.
