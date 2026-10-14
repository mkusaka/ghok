# L2 REST normalization

## API and purity

`rest.Normalize(inv model.Invocation, req model.APIRequest) model.Result` accepts the shared parser's effective method. It uses only `Invocation.Env`, `ReadFile`, and captured `Stdin`; no process environment, filesystem, git, or network access. The existing dispatcher already calls it. No shared contracts or dependencies changed.

The snapshot contains 1,242 OpenAPI operations; 206 operationId overrides retain canonical policy/hook names (205 prior operations plus `repos/merge-upstream`), with 1,036 operations using default `rest.*` identities. All 206 prior route spellings remain supported: contents root listing shares `repos/get-content` with nonempty contents paths. Matching requires the full decoded segment sequence and exact method. Integer schema parameters require positive decimals without overflow; string IDs such as gist IDs stay strings. Workflow IDs also accept `.yml`/`.yaml` filenames. More literal segments win over captures. Terminal slash-bearing captures remain restricted to contents paths and git refs. Candidates are indexed by method, first literal segment, and segment count; terminal captures have a separate bucket.

## Generation and override curation

Source: [`github/rest-api-description`, `ad351b19702b757bfd03a8ee85bcafe717878331`](https://github.com/github/rest-api-description/blob/ad351b19702b757bfd03a8ee85bcafe717878331/descriptions/api.github.com/api.github.com.json), upstream commit date **2026-10-13**. `routes_gen.go` records that SHA/date and each operation's method, path, operationId, path-parameter names/types, tags, and deprecated flag. The stdlib-only generator resolves component parameter/schema references and scalar unions; no runtime or generator module dependencies are added.

From the repository root:

```sh
go generate ./internal/normalize/rest
```

For offline regeneration from the same revision:

```sh
cd internal/normalize/rest
go run ./gen -spec /path/to/api.github.com.json \
  -ref ad351b19702b757bfd03a8ee85bcafe717878331 -date 2026-10-13
```

The network command fetches only the pinned raw JSON; output order is deterministic. `-ref` requires a full SHA and `-date` the source commit date. Local input provenance is the developer's responsibility. Normal builds/tests never fetch or run the generator.

To upgrade, change the SHA/date in `routes.go`'s `go:generate` directive and run it. Curate `overrides.go` by **exact operationId**, not method/path: reuse existing family/action names and keep new generic identities in `rest.*`. Defaults are action `rest.` + operationId with `/` replaced by `.`, family `rest.` + the first operationId segment, GET/HEAD read, POST/PUT/PATCH/DELETE write, all other methods unknown. Deprecated operations retain this method classification.

`TestOverrideContract` requires every override key to exist upstream, rejects duplicate operationIds, and checks the stable family/action digest. Fourteen explicit legacy sharing groups (for example repo/org/environment secrets) are allowed; any other duplicate canonical action fails. Review compatibility before changing that digest or sharing allowlist. Body-driven refinements stay in `params.go`; contents root listing emits `file.list`. Regeneration alone must not rename policy/hook actions.

Notable changes: known codespace writes now emit exact `rest.codespaces.*` instead of unknown writes; search reads emit exact `rest.search.*` instead of heuristic `api.read.unknown`. `repos/merge-upstream` is `branch.merge` / `branch.merge-upstream`, never `pr.merge`. API root `/` is now exact `rest.meta.root` instead of malformed. OpenAPI capture spellings also replace old parameter keys: `environment_name`, `gist_id`, `gpg_key_id`, `ssh_signing_key_id`, `check_run_id`; `origin.route` uses the upstream templates without internal `*` markers.


## Verified upstream behavior

Sources read at cli/cli v2.102.0:

- [`pkg/cmd/api/api.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/api/api.go): default GET, implicit POST for fields/input, explicit method precedence, pagination validation, input body handling, `fillPlaceholders`, and configured default host selection.
- [`pkg/cmd/api/http.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/api/http.go): absolute URLs used verbatim; relative paths appended to RESTPrefix after removing exactly one leading slash; GET fields become query parameters; other methods send JSON; `--input` moves fields into query parameters; nested GET maps flatten to leaf keys and arrays use `[]`; headers do not themselves change the HTTP method.
- [`pkg/cmd/api/fields.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/api/fields.go): raw fields precede typed fields; file/stdin values are literal bytes converted to strings; integer/bool/null conversion precedes placeholder substitution; nested arrays/maps.
- [`internal/ghinstance/host.go`](https://github.com/cli/cli/blob/v2.102.0/internal/ghinstance/host.go): public API prefixes, enterprise `/api/v3/`, tenancy prefixes, and hostname validation.

Source verification is read-only. Tests and dispatcher smoke make no GitHub requests.

## Endpoints and deferred context

- Relative host: `--hostname` > `GH_HOST` > unresolved `target.host`, source `git/config`. Do not assume github.com when gh configuration is unavailable. `GH_REPO` supplies owner/repository placeholders, not the REST request host.
- Absolute `api.github.com` and `uploads.github.com` map to github.com. `api.SUB.ghe.com` maps to SUB.ghe.com. Other hosts with `/api/v3/` map to that enterprise host with the prefix removed. Arbitrary absolute URLs remain unknown, including GET: they need not be GitHub APIs.
- Relative `/api/v3/` is **not** stripped: gh would append it to its existing API prefix. It consequently remains an unregistered route.
- One optional leading slash is supported; `/` denotes the documented API root. Empty endpoints, non-root trailing slashes, double slashes, dot/dot-dot segments (including encoded traversal), invalid escapes, controls, backslashes and URL fragments are rejected conservatively as `malformed-request`, effect unknown. No `path.Clean` or inferred redirects.
- Decode path segments separately. `%2F` stays inside its original segment, allowing encoded branch/environment names and contents paths without turning encoded owner/repository slashes into valid routes. `Source.Path` is the decoded display path; matching uses the preserved segment boundaries.
- Endpoint `{owner}` and `{repo}` and typed field placeholders resolve from `[HOST/]OWNER/REPO` in `GH_REPO`. Missing context remains unresolved, source `git`. Legacy `:owner`, `:repo`, `:branch` syntax is also supported. Raw fields and file contents never undergo placeholder substitution.
- `{branch}` remains deferred to git. Upstream actually rejects it when `GH_REPO` is set; this normalizer retains intended route identity and uncertainty rather than inventing a branch. Unknown brace names stay literal as in gh.

## Parameters and classification

- POST/PUT/PATCH/DELETE fields retain builder types and nested structure. GET fields use gh's wire query representation: string values, flattened nested map keys, repeated `[]` array keys. Endpoint query values are percent-decoded; duplicates become string slices.
- `--input` reads injected file/stdin bytes and extracts only a JSON object. Unavailable, invalid, non-object, or trailing-document input leaves `parameters` unresolved without discarding the route. Fields accompanying input go to the query, not the body.
- The canonical parameter map combines query values and body values; body values take precedence on collisions. Specialization consults **only the body**, so `?state=closed` cannot manufacture a close operation. Non-target route captures (file path, review ID, workflow ID, etc.) are included as parameters and take precedence over payload keys of the same name.
- `merge_method` becomes canonical `parameters.method`, including unresolved metadata. Conventional owner/repo/org, PR/issue numbers, refs/branches, and SHA selectors (`sha`, `commit_sha`, `file_sha`, `tag_sha`, `tree_sha`) populate targets. Existing body-driven PR base/head, git-ref, and workflow dispatch extraction is unchanged. Authenticated-user repo creation has deferred owner/repository rather than an invented user.
- PR/issue PATCH with body state closed/open becomes close/reopen. Typed/JSON `archived: true` becomes repo.archive; raw string `"true"` does not. Review APPROVE/REQUEST_CHANGES/COMMENT are distinct actions; only an **absent** event on a fully available review-create body means pending. Invalid/null/unavailable events remain unresolved.
- Unknown GET/HEAD routes use family `api.read`, action `api.read.unknown`, heuristic confidence, effect read, mutation-free true by GitHub REST convention. Unknown standard non-read methods conservatively have effect write; nonstandard methods have effect unknown. Unrecognized absolute API hosts and malformed paths are never asserted mutation-free.
- `X-HTTP-Method-Override` makes the operation unknown/heuristic with effect unknown. Only its header name is recorded, not values. Accept previews are classification-irrelevant. Output still requires the shared redaction boundary for argv and payload values.
- Explicit non-GET `--paginate`, and `--paginate --input`, are rejected before HTTP by gh. Preserve target/matched Origin and intended action in Detail, but report unknown `malformed-request`, effect unknown, Execution skipped, MutationFree false.
- Important v2.102.0 ordering exception: gh checks pagination **before** implicit POST inference. `gh api repos/o/r/issues --paginate -f title=x` can still send a POST. It stays issue.create/write/will-run with an explanatory Detail; never report it as a rejected or read-only request.

## Coverage and limits

All public API operations in the pinned snapshot have exact route identity, including codespaces, security scanning, search, apps, and deprecated endpoints. The curated subset retains pulls/reviews/issues/repositories/git refs/branch protection/rulesets/contents/releases/actions/secrets/variables/webhooks/keys/gists/orgs/teams/checks/deployments/environments/notifications semantics.

Partial: bodies must be available JSON objects for semantic refinements; git/config/authenticated-user context is deferred; server authorization/validation and actual outcome are not predicted. GHES version-specific availability is not inferred. gh's configurable API host remapping is outside the invocation contract; recorded hosts are canonical host identities, not proof of a transport destination. Query/body collisions intentionally lose the query value in the canonical combined view. JSON numbers follow stdlib decoding (`float64`), while typed `-F` integers follow the shared builder (`int`).

The api.github.com description is the only snapshot. GHES-only routes absent from it remain unknown writes or conventional heuristic GET/HEAD reads; shared public routes can match on GHES, without asserting version-specific availability. No GHES variants are generated. GH_REPO URL forms, arbitrary API remapping, redirects, unknown placeholders, and unusual path spellings are not guessed. Exact identity is not full OpenAPI request validation or confirmed API effect.

All code is portable Go with injected readers; no macOS/Linux-specific implementation. Tests are hermetic and race-compatible. Linux runtime execution was not performed locally; Linux cross-builds are verification of compilation, not runtime behavior.

## Verification

Behavioral tests cover method inference/all standard methods, typed/raw/nested/file/stdin fields, input JSON/unavailable/invalid bodies, placeholders, public/GHES/tenancy hosts, query/path encoding, unknown routes, malformed paths, pagination ordering, headers, and substring/typed-route false positives. Generated-route regressions cover scalar schema references, string IDs, integer/string unions, literal-over-string-capture precedence, deprecated writes, GHES-only unknowns, and target extraction. `FuzzNormalizeREST` exercises arbitrary endpoint/method/field input and mutation-free invariants. `BenchmarkRouteMatching` covers matches, reserved literals, branch merge, and a miss. Actual `ghok normalize -- api ...` smoke exercised generated codespace creation, upstream branch merge, and the preserved squash PR merge action without GitHub requests.

A disposable smoke exercised all 1,242 generated operations through `Normalize`,
checking exact confidence, matched template, and method effect; it was removed.
Network and local-file regeneration produced identical SHA-256
`ee6c2fa4439191f2f34163b9df88cb04cdff6bdaf2655ba5a7f798f2ed04ba0d`.

`go test ./internal/normalize/rest -run '^$' -bench BenchmarkRouteMatching -benchmem -count=3`,
three-run medians on Darwin arm64 / Apple M4 Max (206 hand routes → 1,242 generated):

| Case | Before ns/op | After ns/op | Allocations before → after |
| --- | ---: | ---: | --- |
| PUT PR merge | 1,007 | 634.8 | 338 B / 2 → 336 B / 2 |
| GET review comment | 2,619 | 2,179 | 338 B / 2 → 336 B / 2 |
| POST branch merge | 955.9 | 496.1 | 336 B / 2 → 336 B / 2 |
| PATCH miss | 172.4 | 32.08 | 0 B / 0 → 0 B / 0 |
