# Context resolver

`internal/resolve` resolves environment and git metadata without executing git or ssh. API resolution requires both caller permission and an explicitly requested missing field. A candidate repository with an `Unresolved` entry is not a confirmed target; consumers must inspect resolution status, not just nonempty strings.

## Integration

```go
r := &resolve.Resolver{
    Env: invocation.Env, Cwd: invocation.Cwd,
    RealGh: realGhAbsolutePath, Timeout: resolveTimeout,
}
local := r.Local()
diagnostics := r.Fill(ctx, &result, neededFields, allowAPI)
```

- Public methods: `Local() model.ResolvedContext`; `Fill(context.Context, *model.Result, []string, bool) []string`.
- One resolver per invocation. Configure fields before use and leave them immutable. Calls on distinct results are serialized and race-safe; callers own synchronization for a shared result.
- A non-nil `Env` is the complete subprocess environment. Nil snapshots the process environment on first use. Local metadata, API responses, and failures are cached for that invocation. `Local` returns independent maps/slices.
- `Fill` resolves locally deferred fields even when `need` is empty. Network requests only resolve requested `target.repository`, `target.owner`, or `target.number`; already populated targets are preserved. Diagnostics are returned, not appended to `Result.Errors`.
- L1 current-branch PR selectors populate `HeadBranch`; only a requested missing number can cause the REST lookup. GraphQL node repositories never come from the cwd. Missing GraphQL variables remain unresolved rather than borrowing the local SHA/repository.
- Local provenance is available in `ResolvedContext.Sources` (`git`, `env`, or `default` for the github.com host fallback). The shared `Target` has no provenance map, so `Fill` cannot attach API provenance to populated target fields.
- Classification becomes complete, partial, or unresolved. Unknown operation identity/effect remains unresolved; mutation-free evidence is never changed. Skipped/unselected operations do not cause API calls.

## Supported local metadata

- Parent discovery of `.git` directories/files; relative gitdir pointers for linked worktrees and submodules; `commondir`; nested cwd; explicit `GIT_DIR` and `GIT_WORK_TREE`.
- Basic `GIT_CEILING_DIRECTORIES`: absolute path-list entries stop parent discovery before the ceiling. Existing cwd/ceiling symlinks are canonicalized, including macOS `/var` to `/private/var` aliases.
- Symbolic HEAD, loose/symbolic refs, packed refs, detached SHA-1/SHA-256 HEAD, and unborn branches. An unborn branch retains its name and unresolved SHA.
- Repository-local config: case-insensitive section/key names; case-sensitive quoted subsections; legacy dotted subsections; `#`/`;` comments; quoted/unquoted values; git escapes and continuations; relative and `~/` `include.path`; bounded include recursion; worktree config when enabled.
- Fetch URLs, push-URL fallback, `gh-resolved=base`, and `gh-resolved=OWNER/REPO`. Remote priority is upstream, github, origin, then alphabetical remaining names.
- HTTPS/HTTP, git, ssh/git+ssh, git+https, scp-like URLs, SSH ports, `.git` suffixes, enterprise hosts, and www/ssh.github.com host normalization.
- `GH_REPO` overrides remotes (`OWNER/REPO`, `HOST/OWNER/REPO`, repository URL). Invalid overrides remain unresolved. `GH_HOST` supplies the fallback host; otherwise github.com is the default.
- `branch.<name>.gh-merge-base` supplies a deferred base branch. `branch.<name>.remote` and `.merge` are parsed but do not choose the base repository or imply the PR base branch; the shared context has no tracking-remote fields.
- Basic SSH `Host` patterns/negation and first matching `HostName` from `$HOME/.ssh/config`, including `%h`. No ssh executable is needed.

## Supported API resolution

- REST GET `repos/OWNER/REPO/pulls?head=OWNER:BRANCH&state=open&per_page=2` with URL-escaped query values and explicit `--hostname`/`--method GET`. Exactly one positive PR number is required; zero/multiple matches remain unresolved.
- The fixed read-only GraphQL query resolves PullRequest/Issue numbers and repository ownership, and Repository/Ref repository ownership. Ref names are not treated as full refs or necessarily branches: the query does not distinguish branches from tags. Node type/repository mismatches are rejected.
- Direct absolute `RealGh` execution, the supplied environment plus `GHOK_NESTED=1`, null stdin, 1 MiB stdout and 64 KiB stderr limits, five-second default per-call timeout, parent cancellation, and per-invocation caching. No original user command is executed.
- Helper processes run in a separate Unix process group so timeout/overflow cancellation kills ordinary descendants. Pipe wait time is bounded. Stderr is captured but not exposed in diagnostics because gh/helpers can print credentials.

## Partial/unsupported behavior

- Multiple valid remotes without a resolution marker retain gh's noninteractive candidate **and** `target.repository` unresolved with `multiple remotes; gh may prompt`. PR lookup is withheld for this ambiguity. No interactive repository-network lookup or prompt occurs.
- System/global git config, git environment config injection, URL `insteadOf` rewriting, bare-repository autodiscovery, reftable refs, named-user `~user` includes, and conditional includes are not implemented. Conditional includes produce an explicit unresolved repository. Global/system-only overrides cannot be detected from repository-local metadata.
- SSH system configuration, `Include`, `Match`, dynamic expansion other than `%h`, canonicalization, and executable-based configuration are unsupported. Encountering Include/Match or unsupported HostName expansion leaves the host unresolved rather than substituting `GH_HOST`. SSH configurations outside `$HOME/.ssh/config` are not read.
- Unlike gh's full factory resolver, this local resolver does not inspect authenticated-host configuration or filter remotes by it. Per the resolver contract, `GH_HOST` is a fallback, not a remote filter. Interactive fork-parent discovery and arbitrary branch/issue searches are not performed.
- Metadata files are bounded to 1 MiB each; include and symbolic-ref recursion are bounded to ten nested levels. Malformed/unreadable metadata leaves relevant fields unresolved.
- Unix only: API process-group handling is build-tagged. macOS/Linux use the same implementation; macOS path aliases require canonicalization. Windows is outside project scope.

## Verified upstream behavior

Source inspection, cli/cli v2.102.0:

- [`context/context.go`](https://github.com/cli/cli/blob/v2.102.0/context/context.go): `BaseRepo` honors resolution markers first, picks the first remote without prompting, and uses repository-network discovery when prompting is possible.
- [`context/remote.go`](https://github.com/cli/cli/blob/v2.102.0/context/remote.go): remote priorities; fetch-first/push-fallback translation.
- [`git/objects.go`](https://github.com/cli/cli/blob/v2.102.0/git/objects.go), [`git/client.go`](https://github.com/cli/cli/blob/v2.102.0/git/client.go): `gh-resolved`, tracking branch configuration, and `gh-merge-base` (a PR base-branch preference, not the tracking merge ref).
- [`git/url.go`](https://github.com/cli/cli/blob/v2.102.0/git/url.go), [`internal/ghrepo/repo.go`](https://github.com/cli/cli/blob/v2.102.0/internal/ghrepo/repo.go): scp-style/protocol normalization, SSH port stripping, repository URL parsing, hostname normalization.
- [`pkg/cmdutil/repo_override.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmdutil/repo_override.go): explicit repo override precedes `GH_REPO`.
- [`pkg/cmd/factory/remote_resolver.go`](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/factory/remote_resolver.go): SSH translation and authenticated/default-host filtering. The task's suggested `git/remote.go` and `git/ssh_config.go` do not exist in this release; the implementation lives in the paths above.
- [`cli/go-gh/pkg/ssh/ssh.go`, v2.16.1](https://github.com/cli/go-gh/blob/v2.16.1/pkg/ssh/ssh.go): gh actually translates via `ssh -G`; ghok deliberately implements only filesystem-readable simple aliases instead.

Hermetic fixtures exercise clone/worktree/submodule metadata, environment overrides, detached/packed/unborn HEAD, malformed config, includes, SSH aliases, ambiguity, API permissions/needs, node types, query escaping, cache concurrency, malformed/API-error responses, output overflow, and timeout process termination. `BenchmarkLocal` measures a fresh resolver on a handwritten repository fixture. A temporary executable smoke also exercised the real worktree metadata and fake-gh PR resolution with zero-call and cache assertions; the temporary source/fixtures were removed.
