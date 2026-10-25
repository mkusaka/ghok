(* Model of internal/policy/evaluate.go. Same modeling decisions as
   lean-ghok/Ghok/Policy.lean: Decision is {allow, audit, deny} (warn is allow),
   only considered operations, abstract rule matching (Env), one fold over all
   contributions (fold_app / fold_perm make grouping and order irrelevant). *)
From Stdlib Require Import List Permutation Bool Arith Lia String.
Import ListNotations.

Inductive decision := Allow | Audit | Deny.
Inductive mode := Observe | Enforce.

Definition decision_eq_dec (a b : decision) : {a = b} + {a <> b}.
Proof. decide equality. Defined.

(* evaluate.go:55 *)
Definition merge (a b : decision) : decision :=
  match a, b with
  | Deny, _ | _, Deny => Deny
  | Audit, _ | _, Audit => Audit
  | _, _ => Allow
  end.

Definition fold (xs : list decision) : decision := fold_right merge Allow xs.

(* evaluate.go:206-216 *)
Definition finalize (m : mode) (d : decision) : decision :=
  match m, d with Observe, Deny => Audit | _, _ => d end.

Definition rank (d : decision) : nat :=
  match d with Allow => 0 | Audit => 1 | Deny => 2 end.

Definition mrank (m : mode) : nat := match m with Observe => 0 | Enforce => 1 end.

(* ---- merge is max under Allow < Audit < Deny ---- *)

Lemma merge_comm a b : merge a b = merge b a.
Proof. destruct a, b; reflexivity. Qed.
Lemma merge_assoc a b c : merge (merge a b) c = merge a (merge b c).
Proof. destruct a, b, c; reflexivity. Qed.
Lemma merge_idem a : merge a a = a.
Proof. destruct a; reflexivity. Qed.
Lemma merge_allow_l a : merge Allow a = a.
Proof. destruct a; reflexivity. Qed.
Lemma merge_allow_r a : merge a Allow = a.
Proof. destruct a; reflexivity. Qed.
Lemma merge_deny_r a : merge a Deny = Deny.
Proof. destruct a; reflexivity. Qed.
Lemma merge_max a b : rank (merge a b) = Nat.max (rank a) (rank b).
Proof. destruct a, b; reflexivity. Qed.
Lemma rank_le_two a : rank a <= 2.
Proof. destruct a; simpl; lia. Qed.
Lemma rank_eq_two a : rank a = 2 <-> a = Deny.
Proof. destruct a; simpl; split; congruence. Qed.
Lemma rank_top a : 2 <= rank a -> a = Deny.
Proof. intro; apply rank_eq_two; pose proof (rank_le_two a); lia. Qed.
Lemma rank_inj a b : rank a = rank b -> a = b.
Proof. destruct a, b; simpl; congruence. Qed.

(* ---- the fold ---- *)

Lemma fold_nil : fold [] = Allow.
Proof. reflexivity. Qed.
Lemma fold_cons h t : fold (h :: t) = merge h (fold t).
Proof. reflexivity. Qed.

Lemma fold_app xs ys : fold (xs ++ ys) = merge (fold xs) (fold ys).
Proof.
  induction xs as [|h t IH]; simpl; [destruct (fold ys); reflexivity|].
  unfold fold in *; rewrite IH, merge_assoc; reflexivity.
Qed.

(* "No allow rule can override a deny or audit" (evaluate.go:80) *)
Lemma allow_noop xs : fold (xs ++ [Allow]) = fold xs.
Proof. rewrite fold_app; apply merge_allow_r. Qed.

Lemma allow_anywhere xs ys : fold (xs ++ Allow :: ys) = fold (xs ++ ys).
Proof. rewrite !fold_app, fold_cons, merge_allow_l; reflexivity. Qed.

Lemma fold_monotone xs d : rank (fold xs) <= rank (fold (xs ++ [d])).
Proof. rewrite fold_app, merge_max; lia. Qed.

Lemma rank_fold_cons h t : rank (fold (h :: t)) = Nat.max (rank h) (rank (fold t)).
Proof. apply merge_max. Qed.

Lemma mem_le_fold x xs : In x xs -> rank x <= rank (fold xs).
Proof.
  induction xs as [|h t IH]; [contradiction|].
  rewrite fold_cons, merge_max; intros [-> | H]; [lia|]; specialize (IH H); lia.
Qed.

(* The fold is the maximum; all monotonicity results go through this. *)
Lemma fold_le_of_dominated xs ys :
  (forall x, In x xs -> exists y, In y ys /\ rank x <= rank y) -> rank (fold xs) <= rank (fold ys).
Proof.
  induction xs as [|a t IH]; intro H; [simpl; lia|].
  rewrite fold_cons, merge_max.
  destruct (H a (or_introl eq_refl)) as [y [Hy Hle]].
  pose proof (mem_le_fold _ _ Hy).
  assert (rank (fold t) <= rank (fold ys)) by (apply IH; intros; apply H; now right).
  lia.
Qed.

Lemma deny_wins xs : In Deny xs -> fold xs = Deny.
Proof. intro H; apply rank_top; exact (mem_le_fold _ _ H). Qed.

Lemma would_deny_iff xs : fold xs = Deny <-> In Deny xs.
Proof.
  split; [|apply deny_wins].
  induction xs as [|h t IH]; simpl; [discriminate|].
  unfold fold in *; destruct h, (fold_right merge Allow t) eqn:E;
    simpl; intro H; try discriminate; auto.
Qed.

(* "Rule and operation iteration order is independent of map ordering" *)
Lemma fold_perm xs ys : Permutation xs ys -> fold xs = fold ys.
Proof.
  induction 1; unfold fold in *; simpl.
  - reflexivity.
  - now rewrite IHPermutation.
  - rewrite <- !merge_assoc, (merge_comm y x); reflexivity.
  - congruence.
Qed.

(* ---- observe mode ---- *)

Lemma observe_never_denies d : finalize Observe d <> Deny.
Proof. destruct d; discriminate. Qed.

Lemma finalize_hom m a b : finalize m (merge a b) = merge (finalize m a) (finalize m b).
Proof. destruct m, a, b; reflexivity. Qed.

Lemma finalize_fold m xs : finalize m (fold xs) = fold (map (finalize m) xs).
Proof.
  induction xs as [|h t IH]; [destruct m; reflexivity|].
  simpl map; rewrite !fold_cons, finalize_hom, IH; reflexivity.
Qed.

Lemma finalize_deny_iff m d : finalize m d = Deny <-> m = Enforce /\ d = Deny.
Proof. destruct m, d; simpl; intuition congruence. Qed.

Lemma finalize_mono m m' d d' :
  mrank m <= mrank m' -> rank d <= rank d' -> rank (finalize m d) <= rank (finalize m' d').
Proof. destruct m, m', d, d'; simpl; lia. Qed.

(* ---- Evaluate ---- *)

Inductive kind := KRule | KPolicy | KConfig.

Record op := {
  write : bool;            (* op.Effect != read *)
  unclassified : bool;     (* evaluate.go:76 *)
  inputUnknown : bool;     (* UnknownInputUnavailable / TooLarge *)
  repo : option string;    (* targetValues repo; None is "" *)
  repositoryLess : bool }. (* match.go:138 *)

Record rule := { rid : string; rdecision : decision }.  (* handling(rule.Decision, allow) *)

Record policy := {
  pmode : mode;            (* anything but enforce is observe (evaluate.go:83) *)
  rules : list rule;
  unknownMutation : decision;
  unknownInput : decision;
  protectedUnknown : decision;
  protectedRepos : list string }.

Record subject := { ops : list op; noOps : bool }.

Record env := {
  fires : rule -> op -> bool;
  firesInv : rule -> bool;
  protMatch : string -> string -> bool }.

Definition contrib := (decision * kind)%type.

(* evaluate.go:131-161 *)
Definition ruleContribs (E : env) (s : subject) (r : rule) : list contrib :=
  (if noOps s && firesInv E r then [(rdecision r, KRule)] else []) ++
  map (fun _ => (rdecision r, KRule)) (filter (fires E r) (ops s)).

Definition repoStr (o : op) : string := match repo o with Some r => r | None => ""%string end.
Definition isNone {A} (x : option A) : bool := match x with None => true | _ => false end.

(* evaluate.go:162-182 *)
Definition opContribs (E : env) (p : policy) (o : op) : list contrib :=
  (if inputUnknown o then [(unknownInput p, KPolicy)]
   else if unclassified o then [(unknownMutation p, KPolicy)] else []) ++
  (if (match protectedRepos p with [] => true | _ => false end) || negb (write o) then []
   else if isNone (repo o) && negb (repositoryLess o) then [(protectedUnknown p, KPolicy)]
   else if unclassified o && existsb (fun pat => protMatch E pat (repoStr o)) (protectedRepos p)
   then [(protectedUnknown p, KPolicy)]
   else []).

Definition contribs (E : env) (p : policy) (s : subject) : list contrib :=
  flat_map (ruleContribs E s) (rules p) ++ flat_map (opContribs E p) (ops s).

Record evaluation := { decision_ : decision; wouldDeny : bool; denials : list kind }.

Definition isDeny (d : decision) : bool := match d with Deny => true | _ => false end.

Definition evalOf (m : mode) (cs : list contrib) : evaluation :=
  {| decision_ := finalize m (fold (map fst cs));
     wouldDeny := isDeny (fold (map fst cs));
     denials := map snd (filter (fun c => isDeny (fst c)) cs) |}.

Definition evaluate (E : env) (p : policy) (s : subject) : evaluation :=
  evalOf (pmode p) (contribs E p s).

Lemma isDeny_true d : isDeny d = true <-> d = Deny.
Proof. destruct d; simpl; split; congruence. Qed.

Lemma evalOf_wouldDeny m cs :
  wouldDeny (evalOf m cs) = true <-> exists c, In c cs /\ fst c = Deny.
Proof.
  simpl; rewrite isDeny_true, would_deny_iff, in_map_iff.
  split; intros [c [H1 H2]]; exists c; tauto.
Qed.

(* Every deny leaves a denial, and every denial is a deny. *)
Lemma evalOf_denials m cs : denials (evalOf m cs) <> [] <-> wouldDeny (evalOf m cs) = true.
Proof.
  rewrite evalOf_wouldDeny; simpl; split.
  - destruct (filter (fun c => isDeny (fst c)) cs) as [|c l] eqn:E; [contradiction|].
    intros _; assert (In c (filter (fun c => isDeny (fst c)) cs)) by (rewrite E; now left).
    apply filter_In in H as [H1 H2]; exists c; split; auto; now apply isDeny_true.
  - intros [c [H1 H2]] Hn; apply map_eq_nil in Hn.
    assert (In c (filter (fun c => isDeny (fst c)) cs)) by
      (apply filter_In; split; auto; now apply isDeny_true).
    rewrite Hn in H; contradiction.
Qed.

Lemma evalOf_decision_deny m cs :
  decision_ (evalOf m cs) = Deny <-> m = Enforce /\ wouldDeny (evalOf m cs) = true.
Proof. simpl; rewrite finalize_deny_iff, isDeny_true; tauto. Qed.

Lemma evalOf_observe cs : decision_ (evalOf Observe cs) <> Deny.
Proof. apply observe_never_denies. Qed.

Lemma evaluate_enforce_deny E p s :
  pmode p = Enforce -> (exists c, In c (contribs E p s) /\ fst c = Deny) ->
  decision_ (evaluate E p s) = Deny.
Proof. intros Hm H; apply evalOf_decision_deny; split; auto; now apply evalOf_wouldDeny. Qed.
