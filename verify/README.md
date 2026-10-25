# Machine-checked proofs of ghok's decision pipeline

The same development in three provers, checked by `.github/workflows/proofs.yml`:

| Dir | Prover | Check |
|---|---|---|
| `lean/` | Lean 4.35.0 (`lean-toolchain`) | `cd verify/lean && lake build` |
| `rocq/` | Rocq 9.3 (`From Stdlib`) | `verify/rocq/build.sh` |
| `bend/` | Bend 2.0.39 | `cd verify/bend && bend <file>.bend --verdict` (needs Lean v4.34.0 via `ELAN_TOOLCHAIN`) |

Each has one file per Go area, with the same 78 theorems (Rocq adds 3 helpers):

| File | Go | Proves |
|---|---|---|
| Policy | `internal/policy/evaluate.go` | `merge` is a join-semilattice (allow < audit < deny); the fold ignores grouping and order; observe never denies; Evaluate denies iff some contribution denies; every deny leaves a denial |
| Match | `internal/policy/match.go` | fail-closed matching: a rule that fires after a target resolves already fires before (unless `onUnresolved: ignore`); maybe-matches never exclude; for any glob |
| Config | `internal/config/project.go` `mergeProject` | overlays (`.ghok.yaml`, `.ghok.local.yaml`) never lower any policy field or the evaluated decision |
| Hooks | `internal/hooks/runner.go` | post never denies; a failed pre hook denies under the default `onError`; `continueOnError` never denies; aggregation is order-independent and no allow overrides a deny |
| Decide | `internal/cli/decide_unix.go` | end to end: gh is blocked iff enforce and something denies (rule, unknown*/protected policy, blocked gate, pre hook); exit 77 iff a content denial, else 78; an overlay never unblocks a request |

## What is not proved

These are proofs about a hand-written model of the Go code, linked to it by the
Go line references in each file, not about the Go code itself.

- Configs are assumed post-`ApplyDefaults` and `Validate` (enum-checked mode and decisions).
- Glob matching (`path.Match`) is abstract: results hold for every glob.
- The hook scheduler (concurrency, `dependsOn`, `wait`, cycles, duplicate IDs) is out of model; theorems are about the result list it produces. The overlay theorem assumes the overlay's hook run keeps the user hooks' results.
- Overlays keep the subject fixed: trust-gated project transforms that rewrite argv are out of model.
- Bend encodes propositions as `Bool` equalities and the matcher `Env` as a precomputed finite table (Bend closures are affine), so it additionally assumes that table is built correctly.
