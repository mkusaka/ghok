/-!
Model of the decision side of internal/hooks/runner.go: `effective`
(runner.go:72), `classify` (runner.go:92) and the final aggregation loop
(runner.go:280-298).

The scheduler that produces the results (concurrency, `dependsOn`, `wait`,
cycles, duplicate IDs, skip-after-denial) is out of the model: these theorems
are about whatever result list it hands to aggregation. Every result, including
the runner's own diagnostics, goes through `classify` (runner.go:100-103).
-/

namespace Ghok.Hooks

inductive HDecision | abstain | allow | deny
  deriving DecidableEq, Repr

inductive Outcome | ok | skipped | failed  -- failed: timeout, canceled, invalid response, exit, spawn
  deriving DecidableEq, Repr

/-- `onError`; Go's `default` branch makes "", "deny" and anything else deny. -/
inductive OnError | allow | ignore | deny
  deriving DecidableEq, Repr

inductive Phase | pre | post
  deriving DecidableEq, Repr

structure Hook where
  onError : OnError
  continueOnError : Bool

structure Result where
  outcome : Outcome
  decision : HDecision     -- the hook's response, meaningful when outcome is ok

open HDecision

-- runner.go:72
def effective (ph : Phase) (h : Hook) (r : Result) : HDecision :=
  match ph, r.outcome with
  | .post, _ => abstain
  | .pre, .ok => r.decision
  | .pre, .skipped => abstain
  | .pre, .failed =>
    match h.onError with
    | .allow => allow
    | .ignore => abstain
    | .deny => deny

/-- runner.go:92: the effective decision and `continuedOnError`. -/
def classify (ph : Phase) (h : Hook) (r : Result) : HDecision × Bool :=
  let e := effective ph h r
  if ph == .pre && h.continueOnError && e == deny then (abstain, true) else (e, false)

-- runner.go:280-298, decision only (the message is the first deny's)
def aggregate (ph : Phase) : List HDecision → HDecision
  | [] => abstain
  | e :: es =>
    if ph == .pre && e == deny then deny
    else
      let rest := aggregate ph es
      if rest == abstain && e == allow then allow else rest

/-! ## effective / classify -/

/-- Post decisions are informational only. -/
theorem classify_post h r : (classify .post h r).1 = abstain := by
  simp [classify, effective]

/-- Fail-closed by default: a pre hook that fails to answer denies unless it
    opted into `onError: allow|ignore` or `continueOnError`. -/
theorem classify_failed_deny {h r} (ho : h.onError = .deny) (hc : h.continueOnError = false)
    (hr : r.outcome = .failed) : (classify .pre h r).1 = deny := by
  simp [classify, effective, ho, hc, hr]

/-- An explicit deny from a hook that ran is effective unless continueOnError. -/
theorem classify_ok_deny {h r} (hc : h.continueOnError = false) (hr : r.outcome = .ok)
    (hd : r.decision = deny) : (classify .pre h r).1 = deny := by
  simp [classify, effective, hc, hr, hd]

/-- `continueOnError` hooks never deny, and the flag records exactly when it
    turned a deny into abstain. -/
theorem classify_continue {ph h r} (hc : h.continueOnError = true) :
    (classify ph h r).1 ≠ deny := by
  unfold classify; cases ph
  · by_cases he : effective .pre h r = deny <;> simp [he, hc]
  · simp [effective]

theorem classify_continued_iff {ph h r} :
    (classify ph h r).2 = true ↔
      ph = .pre ∧ h.continueOnError = true ∧ effective ph h r = deny := by
  unfold classify
  cases ph <;> cases hc : h.continueOnError <;> cases he : effective _ h r <;> simp_all [effective]

/-- A skipped hook never decides anything. -/
theorem classify_skipped {ph h r} (hr : r.outcome = .skipped) : (classify ph h r).1 = abstain := by
  cases ph <;> simp [classify, effective, hr]

/-! ## aggregation -/

/-- What the loop computes, as a function of membership only. -/
def spec : Phase → List HDecision → HDecision
  | .pre, es => if deny ∈ es then deny else if allow ∈ es then allow else abstain
  | .post, es => if allow ∈ es then allow else abstain

theorem aggregate_eq_spec ph es : aggregate ph es = spec ph es := by
  induction es with
  | nil => cases ph <;> rfl
  | cons e t ih =>
    simp only [aggregate, ih]
    by_cases hd : deny ∈ t <;> by_cases ha : allow ∈ t <;> cases ph <;> cases e <;> simp [spec, hd, ha]

theorem aggregate_post_ne_deny es : aggregate .post es ≠ deny := by
  rw [aggregate_eq_spec]; simp only [spec]; split <;> simp

/-- In pre, the aggregate denies iff some effective decision is deny:
    no allow can override a denial. -/
theorem aggregate_pre_deny_iff es : aggregate .pre es = deny ↔ deny ∈ es := by
  rw [aggregate_eq_spec]
  by_cases hd : deny ∈ es <;> by_cases ha : allow ∈ es <;> simp [spec, hd, ha]

/-- Completion order does not matter: only membership decides. -/
theorem aggregate_perm {ph es es'} (p : es.Perm es') : aggregate ph es = aggregate ph es' := by
  rw [aggregate_eq_spec, aggregate_eq_spec]
  cases ph <;> simp [spec, p.mem_iff]

/-- Adding hooks never removes a pre denial. -/
theorem aggregate_pre_deny_mono {es es'} (h : ∀ x ∈ es, x ∈ es') :
    aggregate .pre es = deny → aggregate .pre es' = deny := by
  rw [aggregate_pre_deny_iff, aggregate_pre_deny_iff]; exact h _

end Ghok.Hooks
