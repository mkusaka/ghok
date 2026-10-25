/-!
Model of the target part of internal/policy/match.go: `opMatch` (match.go:210),
`excluded` (match.go:242) and the rule loop's use of them (evaluate.go:145).

Glob matching (`MatchRepo`, `path.Match`) stays abstract: every theorem holds for
any `glob`. Criteria that do not read the target (operations, effects,
parameters, considered, invocation and outcome criteria) are `other`, a
predicate on the part of the operation that resolving a target leaves alone.
-/

namespace Ghok.Match

structure Target where
  repo : Option String    -- none: unresolved ("" after targetValues)
  host : Option String
  repoLess : Bool         -- RepositoryLess

structure Op (B : Type) where
  base : B
  t : Target

structure M (B : Type) where
  other : B → Bool
  repos : List String
  hosts : List String

variable {B : Type} (glob : String → String → Bool)

/-- One target criterion: `none` is no match, `some maybe`. -/
def crit (pats : List String) (v : Option String) : Option Bool :=
  if pats.isEmpty then some false
  else match v with
    | none => some true
    | some r => if pats.any (glob · r) then some false else none

def opMatch (m : M B) (o : Op B) : Option Bool :=
  if !m.other o.base then none
  else if !m.repos.isEmpty && o.t.repoLess then none
  else match crit glob m.repos o.t.repo, crit glob m.hosts o.t.host with
    | some a, some b => some (a || b)
    | _, _ => none

/-- "Maybe-matches never exclude" (match.go:239). -/
def excluded (us : List (M B)) (o : Op B) : Bool :=
  us.any fun u => opMatch glob u o == some false

/-- A rule's per-op match after `onUnresolved` and `unless` (evaluate.go:145). -/
def fires (m : M B) (us : List (M B)) (ignoreUnresolved : Bool) (o : Op B) : Bool :=
  match opMatch glob m o with
  | none => false
  | some maybe => !(maybe && ignoreUnresolved) && !excluded glob us o

/-- `o'` is `o` with (some of) its unresolved targets resolved. An unresolved
    repository is never RepositoryLess (match.go:143), so `repoLess` is fixed. -/
structure Refines (o o' : Op B) : Prop where
  base : o.base = o'.base
  repoLess : o.t.repoLess = o'.t.repoLess
  repo : o.t.repo = none ∨ o.t.repo = o'.t.repo
  host : o.t.host = none ∨ o.t.host = o'.t.host

variable {glob}

theorem crit_refine {pats v v'} (h : v = none ∨ v = v') {b} :
    crit glob pats v' = some b → ∃ b', crit glob pats v = some b' ∧ (b = true → b' = true) := by
  rcases h with rfl | rfl
  · intro hb; unfold crit at hb ⊢; by_cases hp : pats.isEmpty <;> simp_all
  · intro hb; exact ⟨b, hb, id⟩

theorem crit_definite {pats v v'} (h : v = none ∨ v = v') :
    crit glob pats v = some false → crit glob pats v' = some false := by
  rcases h with rfl | rfl
  · intro h; unfold crit at h ⊢; by_cases hp : pats.isEmpty <;> simp_all
  · exact id

/-- Resolving a target never turns a match into a non-match. -/
theorem opMatch_refine {m : M B} {o o' : Op B} (r : Refines o o') {b} :
    opMatch glob m o' = some b → ∃ b', opMatch glob m o = some b' ∧ (b = true → b' = true) := by
  unfold opMatch
  rw [r.base, r.repoLess]
  split; · simp
  split; · simp
  split
  · rename_i a c ha hc
    intro hb
    obtain ⟨a', ha', ia⟩ := crit_refine r.repo ha
    obtain ⟨c', hc', ic⟩ := crit_refine r.host hc
    rw [ha', hc']
    refine ⟨a' || c', rfl, ?_⟩
    simp at hb; subst hb
    intro h; simp at h ⊢; rcases h with h | h
    · exact Or.inl (ia h)
    · exact Or.inr (ic h)
  · simp

/-- A definite match stays definite once targets resolve. -/
theorem opMatch_definite {m : M B} {o o' : Op B} (r : Refines o o') :
    opMatch glob m o = some false → opMatch glob m o' = some false := by
  unfold opMatch
  rw [r.base, r.repoLess]
  split; · simp
  split; · simp
  split
  · rename_i a c ha hc
    intro h; simp at h
    rw [crit_definite r.repo (h.1 ▸ ha), crit_definite r.host (h.2 ▸ hc)]
    rfl
  · simp

/-- Exclusion is stable: what `unless` excludes before resolution it still
    excludes after. Hence an unresolved target is never let off by an `unless`
    that might not apply once resolved. -/
theorem excluded_refine {us : List (M B)} {o o' : Op B} (r : Refines o o') :
    excluded glob us o = true → excluded glob us o' = true := by
  simp only [excluded, List.any_eq_true, beq_iff_eq]
  rintro ⟨u, hu, h⟩
  exact ⟨u, hu, opMatch_definite r h⟩

/-- Fail-closed matching: if a rule would fire on the resolved operation, it
    already fires on the unresolved one, unless the rule opted out with
    `onUnresolved: ignore`. Holds for every glob implementation. -/
theorem fires_conservative {m : M B} {us : List (M B)} {o o' : Op B} (r : Refines o o') :
    fires glob m us false o' = true → fires glob m us false o = true := by
  unfold fires
  split
  · simp
  · rename_i b hb
    intro h
    obtain ⟨b', hb', _⟩ := opMatch_refine r hb
    rw [hb']
    simp at h ⊢
    -- exclusion before resolution would persist after it
    cases hx : excluded glob us o
    · rfl
    · have := excluded_refine (us := us) r hx; simp_all

/-- The opt-out is real: `onUnresolved: ignore` drops exactly the maybe-matches. -/
theorem fires_ignore {m : M B} {us : List (M B)} {o : Op B} :
    fires glob m us true o = true ↔ opMatch glob m o = some false ∧ excluded glob us o = false := by
  unfold fires
  split <;> simp_all

end Ghok.Match
