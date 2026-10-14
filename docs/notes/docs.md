# Documentation, CI, and release

## Decisions

- User docs describe current code, not the desired spec. Engineering notes are
  retained as detailed source/test records. No Go/shared-contract/dependency or
  Makefile changes are needed; existing `make build` injects main.version and
  main.commit. No new exported Go API.
- CI uses ubuntu-latest/amd64, ubuntu-24.04-arm/arm64,
  macos-latest/arm64, macos-15-intel/amd64. The explicit Intel label avoids the
  retired macos-13 choice. Native go env assertions detect label architecture
  drift. Labels checked against the [GitHub runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
  on 2026-10-15; latest images are intentionally not OS-version guarantees.
- Every CI job runs vet, gofmt check, normal/race tests, all five existing fuzz
  targets for 10s each, make build and all four CGO-free cross-builds. Tests use
  GOFLAGS=-p=1: the initial concurrent-package local race run, overlapping
  packaging builds, exceeded the existing install doctor test's 3s version
  deadline. Serializing packages does not disable race instrumentation or
  within-package concurrency. The production timeout/code is unchanged.
  Release source verification uses the same package-serialization setting.
- CI contents:read only, cancel superseded runs. Release permissions are granted
  only on its release job: contents:write, id-token:write, attestations:write.
  Checkout does not persist credentials; GH_TOKEN is scoped to publish step.
- Plain shell release workflow rather than GoReleaser: four fixed targets need
  no additional dependency/config. Tag v* triggers source verification, then
  darwin/linux × amd64/arm64 with CGO_ENABLED=0, -trimpath, -buildvcs=false,
  -s/-w and explicit main.version/main.commit. Tag is passed via env and checked
  before being used in filenames/ldflags.
- Archives contain ghok/README/LICENSE in a target-specific directory. GNU tar
  sorts members, normalizes owner/group to 0 and mtime to commit timestamp;
  gzip -n omits timestamp/name. checksums.txt covers all four archives. Flags
  reduce host metadata but do not claim arbitrary-toolchain reproducibility.
- Provenance subjects include all archives and checksums.txt. Attestation must
  succeed before gh release create --verify-tag; no silent unattested fallback.
  Private repo feature availability depends on GitHub plan. No tags/releases
  were pushed/published for verification. No Apple Developer signing/notarization.

## Pinned supply-chain inputs

Resolved directly from GitHub tag refs (commit objects, not annotated-tag IDs):

| Action | Version | Commit |
| --- | --- | --- |
| actions/checkout | v6.0.2 | de0fac2e4500dabe0009e67214ff5f5447ce83dd |
| actions/setup-go | v6.1.0 | 4dc6199c7b1a012772edbd06daecab0f50c9053c |
| actions/attest-build-provenance | v3.0.0 | 977bb373ede98d70efdf65b84cb5f73e068dcc2a |

Sources: [checkout ref](https://api.github.com/repos/actions/checkout/git/ref/tags/v6.0.2),
[setup-go ref](https://api.github.com/repos/actions/setup-go/git/ref/tags/v6.1.0),
[attestation ref](https://api.github.com/repos/actions/attest-build-provenance/git/ref/tags/v3.0.0).
Read the pinned attestation [action.yml](https://github.com/actions/attest-build-provenance/blob/977bb373ede98d70efdf65b84cb5f73e068dcc2a/action.yml):
subject-path accepts a glob, and its composite action's own action dependencies
are commit-pinned. Go and dependency versions remain those already in go.mod/go.sum.

## Verification and platform boundaries

`actionlint .github/workflows/ci.yml .github/workflows/release.yml` validates YAML
and Actions semantics locally. GoReleaser is not installed and not used, so
`goreleaser check` is inapplicable.

Packaging smoke extracts the actual workflow build script into a temporary copy
of cmd/internal/go.mod/go.sum/README/LICENSE. Builds run with the local Go
1.26.5 toolchain. On macOS, a temporary tar command adapter invokes GNU tar in
an existing node:22-bookworm-slim container with --network none/--pull never;
sha256sum adapts to shasum -a 256. No user PATH/config or installed executable
is changed. Script builds all four targets; archive members, uid/gid=0, a single
normalized timestamp, executable extraction and all checksums are verified.
The extracted darwin/arm64 binary reports v0.0.0-docs-smoke and the source commit.
This proves packaging/cross-compilation locally, not hosted OIDC/attestation or
release publication. Native management smoke exercises normalize, explain and
config validate under temporary HOME/XDG/config directories without network.
Installed gh 2.102.0 `attestation verify --help` confirms --repo and
--signer-workflow ([host/]owner/repo/path/to/workflow); the README verification
command selects this release workflow. This help check performs no API call.

Platform process differences are consolidated in compatibility.md from process,
install and resolve notes: Darwin poll/FIFO EOF probes/proc_pidinfo/libSystem
signal restore versus Linux ppoll/procfs/kernel signal syscalls. No new cli/cli
source inspection was needed for this docs area. Existing source-verification
citations are preserved, including cli/cli 2.102.0 pkg/cmd/api/api.go and
http.go, pkg/cmd/pr/merge/merge.go, internal/ghcmd/cmd.go,
pkg/cmd/extension/manager.go and auth credential helper paths in the relevant
native/REST/GraphQL/install/config/resolve records. See those records for exact
inspected paths and partial upstream-equivalence limitations.

## Behavior/spec or contract-comment mismatches (documented, not changed)

- Invalid effective config runs real gh without hooks/logs, even for intended
  enforcement; runtime hook failure handling is not a config fail-closed guarantee.
- Dispatcher selects API only when expanded argv[0] == api; root flags before
  api remain L1. Unknown native flags can retain known family/effect with
  heuristic confidence rather than a single unknown-family record.
- (fixed) REST REQUEST_CHANGES now emits pr.review.request-changes like L1/L3.
- L3 resolution is deferred for every unresolved field, including unavailable
  variables/input; that does not promise the resolver can recover it.
- Multiple GraphQL stdin consumers reuse captured bytes instead of modeling
  sequential reads. REST explicitly tracks stdin consumption.
- Model Source's redaction-safe comment does not describe raw normalizer output:
  redaction occurs at CLI/hook/log/display boundaries.
- Config supplies positive maxOutputBytes=1 MiB for both hook output streams;
  the runner's unconfigured stderr fallback is 64 KiB, not the CLI default.
- Foreground wrapper-directed INT/QUIT is not forwarded; inherited ignored
  signals beyond HUP/INT and wrapper core-dump flags are not exactly transparent.
  Optional capture changes TTY/output ordering; stdin spool can change fd type.
- Local resolver implements a bounded subset of gh git/SSH/auth-host resolution;
  candidate strings with unresolved evidence are not confirmed targets.

Supported: documented CLI lifecycle, known normalizer registries, trusted hook
protocol, Unix targets and local packaging. Partial: resolver/upstream semantic
coverage, capture/signal/stdin transparency, best-effort redaction/logging.
Unsupported: Windows, arbitrary shell/extension analysis, security-boundary
claims, escaped hook-process cleanup, complete inherited dispositions, Apple
notarization, and local proof of hosted release publication.

## Final local check results

- `gofmt -l .`: no output; `go build ./...` and `go vet ./...`: success.
- `GOFLAGS=-p=1 go test -race ./...` and `GOFLAGS=-p=1 go test ./...`:
  all 14 tested packages passed (nine additional packages have no tests).
  `make build` succeeded. The initial default-parallel race run's doctor
  timeout above is retained as a verification limitation, not hidden.
- `actionlint .github/workflows/ci.yml .github/workflows/release.yml`: no
  diagnostics after final workflow changes.
- All local documentation file links resolve; all three hook-protocol JSON
  examples parse, with correct pre/post result presence.
- Each existing fuzz target passed with `-run '^$' -fuzz '^<Name>$'
  -fuzztime 30s`: FuzzParse/apiargs (30.475s), FuzzNormalize/native (30.590s),
  FuzzNormalizeREST/rest (30.563s), FuzzNormalizeGraphQL/graphql (30.646s),
  FuzzRedactString/redact (31.445s). No failing corpus/source changes.
- Packaging/management smoke temporary workspace and executable adapters were
  removed. Generated `bin/ghok` remains the ordinary ignored make-build output.
