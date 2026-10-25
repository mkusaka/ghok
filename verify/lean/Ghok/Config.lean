import Ghok.Policy

/-!
Model of the policy part of `mergeProject` (internal/config/project.go:322),
which layers `.ghok.yaml` and then `.ghok.local.yaml` (local.go:84) over the
user config.

Out of the model: hooks and transforms the overlay adds (trust-gated; a
transform may rewrite argv before policy sees it, so the subject is fixed here),
and `c.Validate()` after the merge, which rejects e.g. cross-layer duplicate
hook IDs instead of letting the runner skip both.
Both configs are post-`ApplyDefaults` (load.go:219), so every field is set.
-/

namespace Ghok
open Decision

/-- Which policy keys the overlay file spells out (`field(policy, key) != nil`). -/
structure Presence where
  mode : Bool
  unknownMutation : Bool
  unknownInput : Bool
  protectedUnknown : Bool

-- project.go:351-367: a lower rank is ignored with a note
def raise (present : Bool) (cur new : Decision) : Decision :=
  if present && !decide (rank new < rank cur) then new else cur

-- project.go:343-349: enforce -> observe is ignored with a note
def overlayMode (present : Bool) (cur new : Mode) : Mode :=
  if present && !(cur == .enforce && new == .observe) then new else cur

def overlay (pr : Presence) (c p : Policy) : Policy where
  mode := overlayMode pr.mode c.mode p.mode
  rules := c.rules ++ p.rules
  unknownMutation := raise pr.unknownMutation c.unknownMutation p.unknownMutation
  unknownInput := raise pr.unknownInput c.unknownInput p.unknownInput
  protectedUnknown := raise pr.protectedUnknown c.protectedUnknown p.protectedUnknown
  protectedRepos := c.protectedRepos ++ p.protectedRepos

theorem raise_ge pr cur new : rank cur ≤ rank (raise pr cur new) := by
  unfold raise; split
  · rename_i h; simp at h; exact h.2
  · exact Nat.le_refl _

theorem overlayMode_ge pr cur new : cur.rank ≤ (overlayMode pr cur new).rank := by
  unfold overlayMode; cases pr <;> cases cur <;> cases new <;> simp [Mode.rank]

/-! ## domination of contribution lists -/

def Dom (xs ys : List Contrib) : Prop := ∀ x ∈ xs, ∃ y ∈ ys, rank x.1 ≤ rank y.1

theorem Dom.refl xs : Dom xs xs := fun x hx => ⟨x, hx, Nat.le_refl _⟩

theorem Dom.append {a a' b b'} (ha : Dom a a') (hb : Dom b b') : Dom (a ++ b) (a' ++ b') := by
  intro x hx
  rcases List.mem_append.1 hx with h | h
  · obtain ⟨y, hy, l⟩ := ha x h; exact ⟨y, List.mem_append_left _ hy, l⟩
  · obtain ⟨y, hy, l⟩ := hb x h; exact ⟨y, List.mem_append_right _ hy, l⟩

theorem Dom.nil ys : Dom [] ys := fun _ h => by cases h

theorem Dom.single {d d' k k'} (h : rank d ≤ rank d') : Dom [(d, k)] [(d', k')] := by
  intro x hx; simp at hx; subst hx; exact ⟨(d', k'), by simp, h⟩

theorem Dom.fold {xs ys} (h : Dom xs ys) :
    rank (Ghok.fold (xs.map Prod.fst)) ≤ rank (Ghok.fold (ys.map Prod.fst)) := by
  apply fold_le_of_dominated
  intro x hx
  obtain ⟨c, hc, rfl⟩ := List.mem_map.1 hx
  obtain ⟨y, hy, l⟩ := h c hc
  exact ⟨y.1, List.mem_map_of_mem hy, l⟩

theorem opContribs_dom (E : Env) (pr : Presence) (c p : Policy) (o : Op) :
    Dom (opContribs E c o) (opContribs E (overlay pr c p) o) := by
  unfold opContribs
  apply Dom.append
  · split
    · exact Dom.single (raise_ge ..)
    · split
      · exact Dom.single (raise_ge ..)
      · exact Dom.nil _
  · by_cases he : c.protectedRepos.isEmpty || !o.write
    · simp only [he, ↓reduceIte]; exact Dom.nil _
    · have he' : ¬((overlay pr c p).protectedRepos.isEmpty || !o.write) := by
        intro h; apply he
        simp [overlay] at h ⊢
        rcases h with ⟨h, _⟩ | h
        · exact Or.inl h
        · exact Or.inr h
      simp only [he, he', Bool.false_eq_true, ↓reduceIte]
      split
      · exact Dom.single (raise_ge ..)
      · split
        · rename_i h2
          have hc : (o.unclassified && (overlay pr c p).protectedRepos.any (E.protMatch · (o.repo.getD ""))) = true := by
            simp only [Bool.and_eq_true] at h2 ⊢
            exact ⟨h2.1, by simp [overlay, List.any_append, h2.2]⟩
          simp only [hc, ↓reduceIte]
          exact Dom.single (raise_ge ..)
        · exact Dom.nil _

theorem contribs_dom (E : Env) (pr : Presence) (c p : Policy) (s : Subject) :
    Dom (contribs E c s) (contribs E (overlay pr c p) s) := by
  unfold contribs
  apply Dom.append
  · intro x hx
    refine ⟨x, ?_, Nat.le_refl _⟩
    obtain ⟨r, hr, hx⟩ := List.mem_flatMap.1 hx
    exact List.mem_flatMap.2 ⟨r, List.mem_append_left _ hr, hx⟩
  · intro x hx
    obtain ⟨o, ho, hx⟩ := List.mem_flatMap.1 hx
    obtain ⟨y, hy, l⟩ := opContribs_dom E pr c p o x hx
    exact ⟨y, List.mem_flatMap.2 ⟨o, ho, hy⟩, l⟩

/-! ## overlays only tighten -/

/-- An overlay never lowers the merged decision before the mode is applied. -/
theorem overlay_fold_ge E pr c p s :
    rank (fold ((contribs E c s).map Prod.fst)) ≤
      rank (fold ((contribs E (overlay pr c p) s).map Prod.fst)) :=
  (contribs_dom E pr c p s).fold

theorem overlay_wouldDeny {E pr c p s} :
    (evaluate E c s).wouldDeny = true → (evaluate E (overlay pr c p) s).wouldDeny = true := by
  simp only [evaluate, evalOf, beq_iff_eq]
  intro h
  have := overlay_fold_ge E pr c p s
  rw [h] at this
  exact rank_eq_two.1 (Nat.le_antisymm (rank_le_two _) this)

/-- "overlays only tighten policy" (layering_fuzz_test.go:13), for every
    subject and every matcher: the final decision never goes down. -/
theorem overlay_decision_ge E pr c p s :
    rank (evaluate E c s).decision ≤ rank (evaluate E (overlay pr c p) s).decision :=
  finalize_mono (overlayMode_ge ..) (overlay_fold_ge E pr c p s)

theorem overlay_keeps_deny {E pr c p s} :
    (evaluate E c s).decision = deny → (evaluate E (overlay pr c p) s).decision = deny := by
  intro h
  have := overlay_decision_ge E pr c p s
  rw [h] at this
  exact rank_eq_two.1 (Nat.le_antisymm (rank_le_two _) this)

/-- user → .ghok.yaml → .ghok.local.yaml. -/
theorem overlay_chain_ge E pr pr' c p l s :
    rank (evaluate E c s).decision ≤
      rank (evaluate E (overlay pr' (overlay pr c p) l) s).decision :=
  Nat.le_trans (overlay_decision_ge E pr c p s) (overlay_decision_ge E pr' _ l s)

end Ghok
