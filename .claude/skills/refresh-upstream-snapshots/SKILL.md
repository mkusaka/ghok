---
name: refresh-upstream-snapshots
description: Refresh ghok's pinned upstream snapshots — the gh (cli/cli) command registry, GitHub's REST OpenAPI description and the public GraphQL schema — then curate whatever the new upstream adds. Use when asked to follow a new gh release, update the OpenAPI/GraphQL snapshot, or when registry_gen.go / routes_gen.go / schema_mutations.go lag behind upstream (e.g. after Renovate bumps internal/normalize/native/gen/go.mod).
---

# Refresh upstream snapshots

ghok classifies gh calls from three checked-in snapshots. Builds and tests never
fetch upstream; only `go generate` does.

| Layer | Generated file | Pin lives in |
| --- | --- | --- |
| L1 native (gh cobra tree) | `internal/normalize/native/registry_gen.go` | `internal/normalize/native/gen/go.mod` (`github.com/cli/cli/v2`) |
| L2 REST (OpenAPI) | `internal/normalize/rest/routes_gen.go` | `//go:generate` in `internal/normalize/rest/routes.go` (`-ref <sha> -date <commit date>`) |
| L3 GraphQL | `internal/normalize/graphql/schema_mutations.go` | `//go:generate` in `internal/normalize/graphql/registry.go` (`-date <fetch date> -sha256 <raw schema sha>`) |

## 1. Compare pins with upstream

```sh
head -3 internal/normalize/native/registry_gen.go   # gh version actually generated
grep cli/cli internal/normalize/native/gen/go.mod  # gh version pinned (Renovate may be ahead)
grep go:generate internal/normalize/rest/routes.go internal/normalize/graphql/registry.go

gh release view -R cli/cli --json tagName,publishedAt
gh api 'repos/github/rest-api-description/commits?path=descriptions/api.github.com/api.github.com.json&per_page=1' \
  --jq '.[0] | {sha, date: .commit.committer.date}'
curl -fsSL https://docs.github.com/public/fpt/schema.docs.graphql | shasum -a 256
```

Check `gh pr list` for an open Renovate PR on the same module first.

## 2. Bump and regenerate

```sh
(cd internal/normalize/native/gen && go get github.com/cli/cli/v2@vX.Y.Z && go mod tidy)
# edit the two //go:generate lines: REST -ref/-date (upstream commit date, YYYY-MM-DD),
# GraphQL -date (today) / -sha256 (hash from step 1)
go generate ./internal/normalize/native ./internal/normalize/graphql ./internal/normalize/rest
```

The GraphQL generator refuses a schema whose hash differs from `-sha256`; if
docs.github.com changed in between, re-hash and update both flags. Each generator
prints its counts — keep them for the docs and the report.

## 3. Fix what the new snapshot breaks

Run `go test ./internal/normalize/... ./test/e2e/...`. Typical failures:

- **`TestRegistryClassifiesEveryCommand`** — new gh commands need curation.
  Read the upstream command source (`go list -m -f '{{.Dir}}' github.com/cli/cli/v2`
  inside `gen/`) for args and side effects, then add:
  - the identity to `internal/normalize/actions/actions.go` (`Family`/`Action`/`Effect`);
  - the command to `internal/normalize/native/semantics.go` (group commands → `help`,
    `positional` matching how the first arg is parsed, e.g. `"issue"` for issue numbers/URLs).
  Classify by real upstream behavior; never loosen an effect to pass a test.
  Downloads that only write local files are `read` (as `release download`).
- **REST tests pinned to a removed operation** — repoint them to an equivalent
  operation that still exists, keeping what the test asserts.
- **New REST operations equivalent to curated native commands** — add them to
  `internal/normalize/rest/overrides.go` so a policy on the native action also
  covers `gh api` (the override test enforces effect = HTTP method).
- New GraphQL mutations need nothing unless a test says so.

## 4. Check hand-ported upstream logic

Some code ports gh source by reading it (api argument parsing, aliases, repo
resolution, install/extension paths, config). Diff the old and new module:

```sh
cd internal/normalize/native/gen
OLD=$(go mod download -json github.com/cli/cli/v2@vOLD | jq -r .Dir)
NEW=$(go mod download -json github.com/cli/cli/v2@vNEW | jq -r .Dir)
diff -rq "$OLD" "$NEW" | grep -v _test.go | grep -E \
  'pkg/cmd/api/|pkg/cmd/root/|context/|/git/|internal/ghrepo|pkg/cmd/factory|internal/ghcmd|pkg/cmd/extension/|gitcredentials|internal/config/config.go|internal/ghinstance|pkg/cmdutil/repo_override'
```

Read each changed file. Behavior changes to ported logic are out of scope for a
snapshot refresh: report them instead of silently shipping.

## 5. Update pinned metadata in docs and messages

```sh
grep -rnE '<old gh ver>|<old rest sha>|<old graphql sha>|<old dates>|<old counts>' --exclude-dir=.git .
```

Update pins, commits, module sums, dates and counts in `docs/normalization.md`
and `docs/notes/{native,rest,graphql,cli}.md`, the gh version in the unknown-flag
message in `internal/normalize/native/native.go`, the `ghok doctor` baseline
(`ghBaseline`/`ghBaselineMinor` in `internal/install/doctor.go` and its tests), the
"baseline is gh X" sentences in `README.md`, `docs/compatibility.md` and
`docs/normalization.md`, and the command/route tables for anything curated in
step 3. Leave "Source inspection at vX" citations and past
benchmark/smoke records alone unless you re-verified them. CHANGELOG is written
at release time, not here.

## 6. Verify

```sh
make fmt && go vet ./... && go test ./... && make lint
# reproducible: a second generate leaves the files byte-identical
shasum internal/normalize/{native/registry_gen.go,graphql/schema_mutations.go,rest/routes_gen.go} > /tmp/s
go generate ./internal/normalize/native ./internal/normalize/graphql ./internal/normalize/rest && shasum -c /tmp/s
make build && ./bin/ghok normalize -- <new command>   # spot-check new curation
```

Secret scanners may flag `h1:` hashes in `gen/go.sum` diffs; they are public
module checksums.

Report per layer: old → new pin, operations/commands added and removed, curation
decisions, and anything from step 4.
