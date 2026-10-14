# Lint cleanup

## Scope and decisions

- Target: golangci-lint v2.14.0, default linters, no configuration file.
- Best-effort cleanup explicitly discards errors: closing read-only files,
  pipe/PTY descriptors and stdin spools; removing temporary artifacts; killing
  and reaping abandoned test helpers. Execution results remain unchanged.
- Human-readable CLI print errors remain intentionally ignored, now explicitly;
  existing JSON/final explanation output error handling is unchanged.
- Capture cancellation keeps best-effort deadlines. Regular files/devices may
  reject deadlines; this existing limitation is documented in
  `internal/process/capture_unix.go`.
- Written shim-state files already check both Write and Close before Rename;
  those checks are unchanged. Tests now check meaningful written-file/logger
  closes, EOF/failure-setup closes, and the streaming-test read deadline.
- Staticcheck rewrites preserve preview order, repository-selector precedence,
  git-config key validation, and PTY wait read order.
- Four Darwin assembly trampoline declarations have individual `nolint:unused`
  comments naming `signal_darwin_arm64.s` and `signal_darwin_amd64.s`.
- No exported API, shared model/protocol/config schema, dependencies, fake
  secrets, or runtime behavior changed. The API parser's preview append is an
  implementation-only rewrite; its signatures and request contract are unchanged.

## Behavior exercised

A temporary, credential-free sandbox ran the actual ghok and fakegh binaries:
GH_REPO fallback versus explicit PR/repository selectors; repeated comma-separated
API previews; local git resolution; byte-exact binary stdout/stderr with a
post-hook; bounded capture metadata; preservation of child exit status 42.
Additional smoke checks covered help, hook listing, explanation output, stdin
GraphQL analysis and enforcing-wrapper byte-exact stdin replay without leaked
spool artifacts.
The sandbox was removed. No real gh command or GitHub API was called.
No cli/cli source was read: this lint-only change introduces no new gh semantics.

## Platforms and limits

Darwin lint runs natively. Linux lint uses `CGO_ENABLED=0 GOOS=linux` because
cross-target cgo analysis otherwise attempts to preprocess Linux C sources with
the macOS toolchain. Linux analysis covers Linux-specific files but is not a
Linux runtime test. No new supported or partially supported command cases;
existing unsupported cases remain unchanged.

## Verification

- Native `golangci-lint run ./...`: 0 issues.
- `CGO_ENABLED=0 GOOS=linux golangci-lint run ./...`: 0 issues.
- `gofmt -l .`: no output.
- `go build ./...` and `go vet ./...`: passed.
- `go test ./...` and `go test -race ./...`: passed when run sequentially.
  An earlier concurrent verification run hit hook/resolver timeouts; no test
  timeouts or unrelated code were changed.
