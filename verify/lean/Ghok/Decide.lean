import Ghok.Policy
import Ghok.Config
import Ghok.Hooks

/-!
Model of internal/cli/decide_unix.go: `evaluateConfig` (trust-blocked gates on
top of policy) and `decide` (policy + pre-hook aggregate → wrapper verdict and
exit code), called with `cfg.Policy.Mode` (wrapper_unix.go:332), the same mode
Evaluate used. The end-to-end theorems are about `run`, the whole pipeline.
-/

namespace Ghok
open Decision
open Hooks (HDecision Hook Result Phase Outcome classify aggregate)

/-- decide_unix.go:80-103. `gate`: some trust-blocked gate hook matches. -/
def evaluateConfig (E : Env) (p : Policy) (s : Subject) (gate : Bool) : Evaluation :=
  let ev := evaluate E p s
  if gate then
    { decision := finalize p.mode deny, wouldDeny := true, denials := ev.denials ++ [.config] }
  else ev

/-- A blocked gate is just one more deny contribution, of kind `config`. -/
theorem evaluateConfig_eq E p s gate :
    evaluateConfig E p s gate =
      evalOf p.mode (contribs E p s ++ if gate then [(deny, .config)] else []) := by
  unfold evaluateConfig evaluate evalOf
  cases gate <;> simp [fold_append, merge_deny_r, show merge deny allow = deny from rfl]

inductive Verdict | allow | audit | wouldDeny | deny
  deriving DecidableEq, Repr

/-- What `decide` reads from a pre-hook result. -/
structure HookOut where
  eff : HDecision
  ok : Bool        -- Outcome == ok: a content denial rather than an error one

/-- decide_unix.go:107-145. `rulesEmpty` is `len(evaluation.Rules) == 0`. -/
def decide (mode : Mode) (ev : Evaluation) (pre : HDecision) (hooks : List HookOut)
    (stdinSkipped rulesEmpty : Bool) : Verdict × Nat :=
  let wd := ev.wouldDeny || ev.decision == deny || pre == .deny
  if wd then
    if mode == .enforce then
      let content := ev.denials.any (· != .config) || hooks.any fun h => h.eff == .deny && h.ok
      (.deny, if content then 77 else 78)
    else (.wouldDeny, 0)
  else if stdinSkipped && rulesEmpty then (.allow, 0)
  else if ev.decision == audit then (.audit, 0)
  else (.allow, 0)

def outs (hs : List (Hook × Result)) : List HookOut :=
  hs.map fun (h, r) => { eff := (classify .pre h r).1, ok := r.outcome == .ok }

/-- The whole pre-gh decision: policy, gates, pre hooks, verdict. -/
def run (E : Env) (p : Policy) (s : Subject) (gate : Bool) (hs : List (Hook × Result))
    (stdinSkipped rulesEmpty : Bool) : Verdict × Nat :=
  decide p.mode (evaluateConfig E p s gate) (aggregate .pre ((outs hs).map (·.eff)))
    (outs hs) stdinSkipped rulesEmpty

/-! ## lemmas -/

theorem ruleContribs_kind {E s r c} (h : c ∈ ruleContribs E s r) : c.2 = .rule := by
  unfold ruleContribs at h
  simp only [List.mem_append, List.mem_map] at h
  rcases h with h | ⟨_, _, rfl⟩
  · split at h <;> simp_all
  · rfl

theorem opContribs_kind {E p o c} (h : c ∈ opContribs E p o) : c.2 = .policy := by
  unfold opContribs at h
  simp only [List.mem_append] at h
  rcases h with h | h <;> (repeat' split at h) <;> simp_all

theorem contribs_kind {E p s c} (h : c ∈ contribs E p s) : c.2 ≠ .config := by
  simp only [contribs, List.mem_append, List.mem_flatMap] at h
  rcases h with ⟨r, -, h⟩ | ⟨o, -, h⟩
  · rw [ruleContribs_kind h]; decide
  · rw [opContribs_kind h]; decide

theorem decision_deny_wouldDeny {m cs} :
    (evalOf m cs).decision = deny → (evalOf m cs).wouldDeny = true :=
  fun h => (evalOf_decision_deny.1 h).2

/-- Policy-side "something denies", gates included. -/
def PolicyDenies (E : Env) (p : Policy) (s : Subject) (gate : Bool) : Prop :=
  (∃ c ∈ contribs E p s, c.1 = deny) ∨ gate = true

def HookDenies (hs : List (Hook × Result)) : Prop :=
  ∃ hr ∈ hs, (classify .pre hr.1 hr.2).1 = .deny

theorem evaluateConfig_wouldDeny {E p s gate} :
    (evaluateConfig E p s gate).wouldDeny = true ↔ PolicyDenies E p s gate := by
  rw [evaluateConfig_eq, evalOf_wouldDeny, PolicyDenies]
  cases gate <;> simp

theorem pre_deny_iff hs :
    aggregate .pre ((outs hs).map (·.eff)) = .deny ↔ HookDenies hs := by
  rw [Hooks.aggregate_pre_deny_iff, HookDenies]
  simp [outs]

theorem wd_iff {E p s gate hs} :
    ((evaluateConfig E p s gate).wouldDeny || (evaluateConfig E p s gate).decision == deny ||
      aggregate .pre ((outs hs).map (·.eff)) == .deny) = true ↔
    PolicyDenies E p s gate ∨ HookDenies hs := by
  rw [← evaluateConfig_wouldDeny, ← pre_deny_iff]
  constructor
  · intro h
    simp only [Bool.or_eq_true, beq_iff_eq] at h
    rcases h with (h | h) | h
    · exact Or.inl h
    · rw [evaluateConfig_eq] at h ⊢; exact Or.inl (decision_deny_wouldDeny h)
    · exact Or.inr h
  · intro h; simp only [Bool.or_eq_true, beq_iff_eq]
    rcases h with h | h
    · exact Or.inl (Or.inl h)
    · exact Or.inr h

/-! ## end-to-end -/

/-- gh is blocked iff the mode is enforce and something denies: a policy
    rule, unknownMutation/unknownInput/protectedUnknown, a blocked gate, or a
    pre hook (an explicit deny, or a failure under the default onError). -/
theorem run_deny_iff E p s gate hs ss re :
    (run E p s gate hs ss re).1 = .deny ↔
      p.mode = .enforce ∧ (PolicyDenies E p s gate ∨ HookDenies hs) := by
  unfold run decide
  simp only
  split
  · rename_i hwd
    rw [wd_iff] at hwd
    cases hm : p.mode <;> simp_all
  · rename_i hwd
    rw [wd_iff] at hwd
    have : ¬(p.mode = .enforce ∧ (PolicyDenies E p s gate ∨ HookDenies hs)) := fun h => hwd h.2
    simp only [this, iff_false]
    repeat' split
    all_goals simp

/-- Observe never blocks gh, whatever denies. -/
theorem run_observe {E p s gate hs ss re} (hm : p.mode = .observe) :
    (run E p s gate hs ss re).1 ≠ .deny := by
  rw [Ne, run_deny_iff, hm]; simp

/-- The stdin relabel (and everything else) can never hide a denial. -/
theorem run_enforce_blocks {E p s gate hs ss re} (hm : p.mode = .enforce)
    (h : PolicyDenies E p s gate ∨ HookDenies hs) : (run E p s gate hs ss re).1 = .deny :=
  (run_deny_iff ..).2 ⟨hm, h⟩

/-- Fail-closed end to end: a pre hook that fails with the default onError
    blocks gh in enforce mode. -/
theorem run_failed_hook_blocks {E p s gate hs ss re h r} (hm : p.mode = .enforce)
    (hin : (h, r) ∈ hs) (ho : h.onError = .deny) (hc : h.continueOnError = false)
    (hr : r.outcome = .failed) : (run E p s gate hs ss re).1 = .deny :=
  run_enforce_blocks hm (Or.inr ⟨(h, r), hin, Hooks.classify_failed_deny ho hc hr⟩)

/-- Exit codes (docs/hook-protocol.md:364): a block exits 77 when some denial
    is about content (a policy rule/policy check, or a hook that ran and said
    deny), else 78 (only hook errors or gates). Anything else exits 0 here. -/
theorem run_exit E p s gate hs ss re :
    (run E p s gate hs ss re).2 =
      if (run E p s gate hs ss re).1 = .deny then
        if (∃ c ∈ contribs E p s, c.1 = deny) ∨
            (∃ hr ∈ hs, (classify .pre hr.1 hr.2).1 = .deny ∧ hr.2.outcome = .ok)
        then 77 else 78
      else 0 := by
  unfold run decide
  simp only
  split
  · split
    · simp only [ite_true]
      congr 1
      rw [evaluateConfig_eq]
      apply propext
      simp only [evalOf, Bool.or_eq_true, List.any_eq_true, List.mem_map, List.mem_filter,
        List.mem_append, bne_iff_ne, ne_eq, beq_iff_eq, Bool.and_eq_true, outs]
      constructor
      · rintro (⟨k, ⟨c, ⟨hc, hd⟩, rfl⟩, hk⟩ | ⟨o, ⟨hr, hhr, rfl⟩, he, hok⟩)
        · rcases hc with hc | hc
          · exact Or.inl ⟨c, hc, by simpa using hd⟩
          · cases gate <;> simp_all
        · exact Or.inr ⟨hr, hhr, he, by simpa using hok⟩
      · rintro (⟨c, hc, hd⟩ | ⟨hr, hhr, he, hok⟩)
        · exact Or.inl ⟨c.2, ⟨c, ⟨Or.inl hc, by simp [hd]⟩, rfl⟩, contribs_kind hc⟩
        · exact Or.inr ⟨_, ⟨hr, hhr, rfl⟩, he, by simp [hok]⟩
    · simp
  · repeat' split
    all_goals simp_all

/-- Overlays only tighten, end to end: a request the user config blocks stays
    blocked under any project/local overlay, provided the overlay's hook run
    still contains the user hooks' results (the scheduler is out of model)
    and a gate blocked before stays blocked. -/
theorem run_overlay_keeps_deny {E pr c p s gate gate' hs hs' ss re ss' re'}
    (hgate : gate = true → gate' = true) (hh : ∀ x ∈ hs, x ∈ hs') :
    (run E c s gate hs ss re).1 = .deny → (run E (overlay pr c p) s gate' hs' ss' re').1 = .deny := by
  rw [run_deny_iff, run_deny_iff]
  rintro ⟨hm, hd⟩
  refine ⟨?_, ?_⟩
  · have := overlayMode_ge pr.mode c.mode p.mode
    rw [hm] at this
    show overlayMode _ _ _ = _
    cases h : overlayMode pr.mode c.mode p.mode <;> simp_all [Mode.rank]
  · rcases hd with (⟨x, hx, hxd⟩ | hg) | ⟨hr, hin, hdeny⟩
    · -- the overlay's contributions dominate the user's
      obtain ⟨y, hy, hle⟩ := contribs_dom E pr c p s x hx
      rw [hxd] at hle
      exact Or.inl (Or.inl ⟨y, hy, rank_eq_two.1 (Nat.le_antisymm (rank_le_two _) hle)⟩)
    · exact Or.inl (Or.inr (hgate hg))
    · exact Or.inr ⟨hr, hh _ hin, hdeny⟩

end Ghok
