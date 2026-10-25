/-!
Model of internal/policy/evaluate.go.

Modeling decisions:
* `Decision` is {allow, audit, deny}; `abstain` never reaches the policy engine.
  A rule's `warn` goes through `handling`'s fallback and is `allow`
  (evaluate.go:64, config enum `deny,audit,warn`).
* Only considered operations are modeled; `opMatch` (match.go:211) and the
  per-op loops skip the others.
* Rule matching is abstract (`Env.fires`): it already includes `opMatch`,
  `onUnresolved: ignore` and `unless` exclusion. `Ghok.Match` proves what is
  known about it.
* Go merges rule results into `e.Decision` and op results into `PerOp`, then
  merges `PerOp` into `e.Decision`. The model folds one list of contributions;
  `fold_append` and `fold_perm` make the grouping and order irrelevant.
-/

namespace Ghok

inductive Decision | allow | audit | deny
  deriving DecidableEq, Repr

inductive Mode | observe | enforce
  deriving DecidableEq, Repr

open Decision

-- evaluate.go:55
def merge : Decision → Decision → Decision
  | deny, _ | _, deny => deny
  | audit, _ | _, audit => audit
  | _, _ => allow

def fold (xs : List Decision) : Decision := xs.foldr merge allow

-- evaluate.go:206-216: observe turns a would-be deny into audit
def finalize : Mode → Decision → Decision
  | .observe, deny => audit
  | _, d => d

def rank : Decision → Nat
  | allow => 0 | audit => 1 | deny => 2

def Mode.rank : Mode → Nat
  | .observe => 0 | .enforce => 1

/-! ## merge is a join-semilattice: max under allow < audit < deny -/

theorem merge_comm a b : merge a b = merge b a := by cases a <;> cases b <;> rfl
theorem merge_assoc a b c : merge (merge a b) c = merge a (merge b c) := by
  cases a <;> cases b <;> cases c <;> rfl
theorem merge_idem a : merge a a = a := by cases a <;> rfl
theorem merge_allow_l a : merge allow a = a := by cases a <;> rfl
theorem merge_allow_r a : merge a allow = a := by cases a <;> rfl
theorem merge_deny_r a : merge a deny = deny := by cases a <;> rfl
theorem merge_max a b : rank (merge a b) = max (rank a) (rank b) := by
  cases a <;> cases b <;> rfl
theorem rank_inj {a b} : rank a = rank b → a = b := by
  cases a <;> cases b <;> simp [rank]
theorem rank_le_two a : rank a ≤ 2 := by cases a <;> simp [rank]
theorem rank_eq_two {a} : rank a = 2 ↔ a = deny := by cases a <;> simp [rank]

/-! ## the fold -/

@[simp] theorem fold_nil : fold [] = allow := rfl
@[simp] theorem fold_cons h t : fold (h :: t) = merge h (fold t) := rfl

theorem fold_append xs ys : fold (xs ++ ys) = merge (fold xs) (fold ys) := by
  induction xs with
  | nil => simp [merge_allow_l]
  | cons h t ih => simp [ih, merge_assoc]

-- "No allow rule can override a deny or audit" (evaluate.go:80)
theorem allow_noop xs : fold (xs ++ [allow]) = fold xs := by
  rw [fold_append]; exact merge_allow_r _

theorem allow_anywhere xs ys : fold (xs ++ allow :: ys) = fold (xs ++ ys) := by
  simp [fold_append, merge_allow_l]

theorem fold_monotone xs d : rank (fold xs) ≤ rank (fold (xs ++ [d])) := by
  rw [fold_append, merge_max]; exact Nat.le_max_left _ _

theorem rank_fold_cons h t : rank (fold (h :: t)) = max (rank h) (rank (fold t)) := by
  simp [merge_max]

theorem mem_le_fold {x xs} : x ∈ xs → rank x ≤ rank (fold xs) := by
  induction xs with
  | nil => intro h; cases h
  | cons h t ih =>
    intro hm
    rw [rank_fold_cons]
    cases hm with
    | head => exact Nat.le_max_left _ _
    | tail _ hm => exact Nat.le_trans (ih hm) (Nat.le_max_right _ _)

/-- The fold is the maximum: each contribution of `xs` dominated by some
    contribution of `ys` makes `fold xs ≤ fold ys`. All monotonicity results
    (overlay, gates, hooks) go through this. -/
theorem fold_le_of_dominated {xs ys : List Decision}
    (h : ∀ x ∈ xs, ∃ y ∈ ys, rank x ≤ rank y) : rank (fold xs) ≤ rank (fold ys) := by
  induction xs with
  | nil => simp [rank]
  | cons a t ih =>
    rw [rank_fold_cons]
    obtain ⟨y, hy, hle⟩ := h a (List.mem_cons_self ..)
    exact Nat.max_le.2 ⟨Nat.le_trans hle (mem_le_fold hy),
      ih fun x hx => h x (List.mem_cons_of_mem _ hx)⟩

theorem deny_wins {xs} : deny ∈ xs → fold xs = deny := by
  intro h
  have := mem_le_fold h
  exact rank_eq_two.1 (Nat.le_antisymm (rank_le_two _) this)

theorem would_deny_iff xs : fold xs = deny ↔ deny ∈ xs := by
  refine ⟨fun hd => ?_, deny_wins⟩
  induction xs with
  | nil => cases hd
  | cons a t ih =>
    simp only [fold_cons, List.mem_cons] at *
    cases a <;> cases e : fold t <;> simp_all [merge]

-- "Rule and operation iteration order is independent of map ordering"
theorem fold_perm {xs ys} (p : xs.Perm ys) : fold xs = fold ys := by
  induction p with
  | nil => rfl
  | cons x _ ih => simp [ih]
  | swap x y l => simp only [fold_cons]; rw [← merge_assoc, ← merge_assoc, merge_comm y x]
  | trans _ _ ih1 ih2 => exact ih1.trans ih2

/-! ## observe mode -/

theorem observe_never_denies d : finalize .observe d ≠ deny := by cases d <;> decide

theorem finalize_hom m a b : finalize m (merge a b) = merge (finalize m a) (finalize m b) := by
  cases m <;> cases a <;> cases b <;> rfl

-- the e.Decision and e.PerOp downgrades agree
theorem finalize_fold m xs : finalize m (fold xs) = fold (xs.map (finalize m)) := by
  induction xs with
  | nil => cases m <;> rfl
  | cons h t ih => simp [finalize_hom, ih]

theorem finalize_deny_iff {m d} : finalize m d = deny ↔ m = .enforce ∧ d = deny := by
  cases m <;> cases d <;> simp [finalize]

/-- finalize is monotone in both the mode and the decision. -/
theorem finalize_mono {m m' d d'} (hm : m.rank ≤ m'.rank) (hd : rank d ≤ rank d') :
    rank (finalize m d) ≤ rank (finalize m' d') := by
  cases m <;> cases m' <;> cases d <;> cases d' <;> simp_all [finalize, rank, Mode.rank]

/-! ## Evaluate -/

/-- Who caused a contribution; becomes `Denial.Kind`. `config` is the
    trust-blocked gate added by `evaluateConfig` (cli/decide_unix.go:80). -/
inductive Kind | rule | policy | config
  deriving DecidableEq, Repr

/-- A considered operation, reduced to what Evaluate's policy checks read. -/
structure Op where
  write : Bool            -- op.Effect != read
  unclassified : Bool     -- evaluate.go:76
  inputUnknown : Bool     -- UnknownInputUnavailable / UnknownInputTooLarge
  repo : Option String    -- targetValues repo; none is ""
  repositoryLess : Bool   -- match.go:138

structure Rule where
  id : String
  decision : Decision     -- handling(rule.Decision, allow)

structure Policy where
  mode : Mode             -- anything but enforce is observe (evaluate.go:83)
  rules : List Rule
  unknownMutation : Decision
  unknownInput : Decision
  protectedUnknown : Decision
  protectedRepos : List String

structure Subject where
  ops : List Op           -- considered operations
  noOps : Bool            -- len(s.Result.Operations) == 0

/-- Matching, abstracted (see `Ghok.Match`). -/
structure Env where
  fires : Rule → Op → Bool     -- definite or conservative per-op match, not excluded
  firesInv : Rule → Bool       -- Matches with no operations
  protMatch : String → String → Bool  -- MatchRepo

abbrev Contrib := Decision × Kind

-- evaluate.go:131-161
def ruleContribs (E : Env) (s : Subject) (r : Rule) : List Contrib :=
  (if s.noOps && E.firesInv r then [(r.decision, .rule)] else []) ++
  (s.ops.filter (E.fires r)).map fun _ => (r.decision, .rule)

-- evaluate.go:162-182
def opContribs (E : Env) (p : Policy) (o : Op) : List Contrib :=
  (if o.inputUnknown then [(p.unknownInput, .policy)]
   else if o.unclassified then [(p.unknownMutation, .policy)] else []) ++
  (if p.protectedRepos.isEmpty || !o.write then []
   else if o.repo.isNone && !o.repositoryLess then [(p.protectedUnknown, .policy)]
   else if o.unclassified && p.protectedRepos.any (E.protMatch · (o.repo.getD "")) then
     [(p.protectedUnknown, .policy)]
   else [])

def contribs (E : Env) (p : Policy) (s : Subject) : List Contrib :=
  p.rules.flatMap (ruleContribs E s) ++ s.ops.flatMap (opContribs E p)

structure Evaluation where
  decision : Decision
  wouldDeny : Bool
  denials : List Kind

def evalOf (m : Mode) (cs : List Contrib) : Evaluation :=
  { decision := finalize m (fold (cs.map Prod.fst))
    wouldDeny := fold (cs.map Prod.fst) == deny
    denials := (cs.filter (·.1 == deny)).map Prod.snd }

def evaluate (E : Env) (p : Policy) (s : Subject) : Evaluation :=
  evalOf p.mode (contribs E p s)

theorem evalOf_wouldDeny {m cs} :
    (evalOf m cs).wouldDeny = true ↔ ∃ c ∈ cs, c.1 = deny := by
  simp [evalOf, would_deny_iff]

/-- Every deny leaves a denial, and every denial is a deny. -/
theorem evalOf_denials {m cs} :
    (evalOf m cs).denials ≠ [] ↔ (evalOf m cs).wouldDeny = true := by
  rw [evalOf_wouldDeny]; simp [evalOf, List.filter_eq_nil_iff]

theorem evalOf_decision_deny {m cs} :
    (evalOf m cs).decision = deny ↔ m = .enforce ∧ (evalOf m cs).wouldDeny = true := by
  simp [evalOf, finalize_deny_iff]

theorem evalOf_observe {cs} : (evalOf .observe cs).decision ≠ deny :=
  observe_never_denies _

/-- In enforce mode, any deny contribution (a rule, unknownMutation,
    unknownInput, protectedUnknown) makes the evaluation deny. -/
theorem evaluate_enforce_deny {E p s} (hm : p.mode = .enforce)
    (h : ∃ c ∈ contribs E p s, c.1 = deny) : (evaluate E p s).decision = deny := by
  unfold evaluate
  exact evalOf_decision_deny.2 ⟨hm, evalOf_wouldDeny.2 h⟩

end Ghok
