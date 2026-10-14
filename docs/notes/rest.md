# L2 REST normalization

## API and purity

`rest.Normalize(inv model.Invocation, req model.APIRequest) model.Result` accepts the shared parser's effective method. It uses only `Invocation.Env`, `ReadFile`, and captured `Stdin`; no process environment, filesystem, git, or network access. The existing dispatcher already calls it. No shared contracts or dependencies changed.

The registry contains 206 method/path pairs. Matching requires the full sequence of decoded segments, positive decimal PR/issue numbers and resource IDs, and exact method. Workflow IDs also accept `.yml`/`.yaml` filenames. More literal segments win over parameter segments. Terminal captures are restricted to documented contents paths and git refs. Matching an unknown route never implies a known mutation.

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
- One optional leading slash is supported. Empty paths, trailing slashes, double slashes, dot/dot-dot segments (including encoded traversal), invalid escapes, controls, backslashes and URL fragments are rejected conservatively as `malformed-request`, effect unknown. No `path.Clean` or inferred redirects.
- Decode path segments separately. `%2F` stays inside its original segment, allowing encoded branch/environment names and contents paths without turning encoded owner/repository slashes into valid routes. `Source.Path` is the decoded display path; matching uses the preserved segment boundaries.
- Endpoint `{owner}` and `{repo}` and typed field placeholders resolve from `[HOST/]OWNER/REPO` in `GH_REPO`. Missing context remains unresolved, source `git`. Legacy `:owner`, `:repo`, `:branch` syntax is also supported. Raw fields and file contents never undergo placeholder substitution.
- `{branch}` remains deferred to git. Upstream actually rejects it when `GH_REPO` is set; this normalizer retains intended route identity and uncertainty rather than inventing a branch. Unknown brace names stay literal as in gh.

## Parameters and classification

- POST/PUT/PATCH/DELETE fields retain builder types and nested structure. GET fields use gh's wire query representation: string values, flattened nested map keys, repeated `[]` array keys. Endpoint query values are percent-decoded; duplicates become string slices.
- `--input` reads injected file/stdin bytes and extracts only a JSON object. Unavailable, invalid, non-object, or trailing-document input leaves `parameters` unresolved without discarding the route. Fields accompanying input go to the query, not the body.
- The canonical parameter map combines query values and body values; body values take precedence on collisions. Specialization consults **only the body**, so `?state=closed` cannot manufacture a close operation. Non-target route captures (file path, review ID, workflow ID, etc.) are included as parameters and take precedence over payload keys of the same name.
- `merge_method` becomes canonical `parameters.method`, including unresolved metadata. Repository/PR/issue targets, refs, branch-protection branches, SHAs, PR base/head, and workflow dispatch refs are extracted when known. Authenticated-user repo creation has deferred owner/repository rather than an invented user.
- PR/issue PATCH with body state closed/open becomes close/reopen. Typed/JSON `archived: true` becomes repo.archive; raw string `"true"` does not. Review APPROVE/REQUEST_CHANGES/COMMENT are distinct actions; only an **absent** event on a fully available review-create body means pending. Invalid/null/unavailable events remain unresolved.
- Unknown GET/HEAD routes use family `api.read`, action `api.read.unknown`, heuristic confidence, effect read, mutation-free true by GitHub REST convention. Unknown standard non-read methods conservatively have effect write; nonstandard methods have effect unknown. Unrecognized absolute API hosts and malformed paths are never asserted mutation-free.
- `X-HTTP-Method-Override` makes the operation unknown/heuristic with effect unknown. Only its header name is recorded, not values. Accept previews are classification-irrelevant. Output still requires the shared redaction boundary for argv and payload values.
- Explicit non-GET `--paginate`, and `--paginate --input`, are rejected before HTTP by gh. Preserve target/matched Origin and intended action in Detail, but report unknown `malformed-request`, effect unknown, Execution skipped, MutationFree false.
- Important v2.102.0 ordering exception: gh checks pagination **before** implicit POST inference. `gh api repos/o/r/issues --paginate -f title=x` can still send a POST. It stays issue.create/write/will-run with an explanatory Detail; never report it as a rejected or read-only request.

## Coverage and limits

Supported registry areas: pulls/merge/reviews/review submission/dismissal/requested reviewers/update branch/review comments; issues/comments/labels/assignees/locks; repository creation/update/archive/delete/transfer/forks/collaborators; git refs; branch protection; repo/org rulesets; contents; releases/assets; workflow dispatch/enable/disable; action runs/jobs; repo/org/environment secrets and variables; webhooks; deploy/user/GPG/signing keys; gists/comments; org memberships; teams/membership/repository access; branch merges; checks/statuses; deployments/statuses; environments; user update; notification marking.

Partial: bodies must be available JSON objects for semantic refinements; git/config/authenticated-user context is deferred; server authorization/validation and actual outcome are not predicted. GHES version-specific availability is not inferred. gh's configurable API host remapping is outside the invocation contract; recorded hosts are canonical host identities, not proof of a transport destination. Query/body collisions intentionally lose the query value in the canonical combined view. JSON numbers follow stdlib decoding (`float64`), while typed `-F` integers follow the shared builder (`int`).

Unsupported registry routes, including codespaces, remain explicit unknown writes or conventional unknown reads. GH_REPO URL forms, arbitrary API remapping, redirects, unknown placeholders, unusual path spellings, and full OpenAPI coverage are not guessed. No confirmed API effect is claimed merely from intent.

All code is portable Go with injected readers; no macOS/Linux-specific implementation. Tests are hermetic and race-compatible. Linux runtime execution was not performed locally; Linux cross-builds are verification of compilation, not runtime behavior.

## Verification

Behavioral tests cover method inference/all standard methods, typed/raw/nested/file/stdin fields, input JSON/unavailable/invalid bodies, placeholders, public/GHES/tenancy hosts, query/path encoding, unknown routes, malformed paths, pagination ordering, headers, and substring/typed-route false positives. `FuzzNormalizeREST` exercises arbitrary endpoint/method/field input and mutation-free invariants. `BenchmarkRouteMatching` covers matches, reserved literals, branch merge, and a miss. A temporary program exercised six scenarios through the real shared dispatcher and was removed after passing.
