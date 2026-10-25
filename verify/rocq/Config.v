(* Model of the policy part of mergeProject (internal/config/project.go:322),
   applied for .ghok.yaml and then .ghok.local.yaml (local.go:84).
   Out of model: overlay hooks/transforms (trust-gated; the subject is fixed)
   and the post-merge Validate. Configs are post-ApplyDefaults (load.go:219). *)
From Stdlib Require Import List Bool Arith Lia String.
From Ghok Require Import Policy.
Import ListNotations.

Record presence := { pMode : bool; pUM : bool; pUI : bool; pPU : bool }.

(* project.go:351-367: a lower rank is ignored with a note *)
Definition raise (present : bool) (cur new : decision) : decision :=
  if present && negb (Nat.ltb (rank new) (rank cur)) then new else cur.

(* project.go:343-349: enforce -> observe is ignored with a note *)
Definition overlayMode (present : bool) (cur new : mode) : mode :=
  if present && negb (match cur, new with Enforce, Observe => true | _, _ => false end)
  then new else cur.

Definition overlay (pr : presence) (c p : policy) : policy :=
  {| pmode := overlayMode (pMode pr) (pmode c) (pmode p);
     rules := rules c ++ rules p;
     unknownMutation := raise (pUM pr) (unknownMutation c) (unknownMutation p);
     unknownInput := raise (pUI pr) (unknownInput c) (unknownInput p);
     protectedUnknown := raise (pPU pr) (protectedUnknown c) (protectedUnknown p);
     protectedRepos := protectedRepos c ++ protectedRepos p |}.

Lemma raise_ge pr cur new : rank cur <= rank (raise pr cur new).
Proof.
  unfold raise; destruct pr, (Nat.ltb (rank new) (rank cur)) eqn:E; simpl; try lia.
  apply Nat.ltb_ge in E; lia.
Qed.

Lemma overlayMode_ge pr cur new : mrank cur <= mrank (overlayMode pr cur new).
Proof. destruct pr, cur, new; simpl; lia. Qed.

(* ---- domination of contribution lists ---- *)

Definition Dom (xs ys : list contrib) : Prop :=
  forall x, In x xs -> exists y, In y ys /\ rank (fst x) <= rank (fst y).

Lemma Dom_refl xs : Dom xs xs.
Proof. intros x Hx; exists x; auto. Qed.

Lemma Dom_app a a' b b' : Dom a a' -> Dom b b' -> Dom (a ++ b) (a' ++ b').
Proof.
  intros Ha Hb x Hx; apply in_app_or in Hx as [H|H].
  - destruct (Ha x H) as [y [Hy L]]; exists y; split; auto; apply in_or_app; auto.
  - destruct (Hb x H) as [y [Hy L]]; exists y; split; auto; apply in_or_app; auto.
Qed.

Lemma Dom_nil ys : Dom [] ys.
Proof. intros x []. Qed.

Lemma Dom_single d d' k k' : rank d <= rank d' -> Dom [(d, k)] [(d', k')].
Proof. intros L x [<- | []]; exists (d', k'); simpl; auto. Qed.

Lemma Dom_fold xs ys : Dom xs ys -> rank (fold (map fst xs)) <= rank (fold (map fst ys)).
Proof.
  intro H; apply fold_le_of_dominated; intros x Hx.
  apply in_map_iff in Hx as [c [<- Hc]].
  destruct (H c Hc) as [y [Hy L]]; exists (fst y); split; auto; now apply in_map.
Qed.

Lemma opContribs_dom E pr c p o : Dom (opContribs E c o) (opContribs E (overlay pr c p) o).
Proof.
  unfold opContribs; apply Dom_app.
  - destruct (inputUnknown o); [apply Dom_single, raise_ge|].
    destruct (unclassified o); [apply Dom_single, raise_ge | apply Dom_nil].
  - unfold overlay; cbn [protectedRepos]; rewrite existsb_app.
    destruct (existsb (fun pat => protMatch E pat (repoStr o)) (protectedRepos c)) eqn:Hx;
      destruct (protectedRepos c) as [|pc pcs];
      try apply Dom_nil;
      cbn -[existsb]; destruct (write o); cbn -[existsb]; try apply Dom_nil;
      destruct (isNone (repo o) && negb (repositoryLess o)); try (apply Dom_single, raise_ge);
      rewrite ?Hx; destruct (unclassified o); cbn -[existsb]; rewrite ?Hx; cbn;
      try apply Dom_nil; try (apply Dom_single, raise_ge).
Qed.

Lemma contribs_dom E pr c p s : Dom (contribs E c s) (contribs E (overlay pr c p) s).
Proof.
  unfold contribs; apply Dom_app.
  - intros x Hx; exists x; split; auto.
    apply in_flat_map in Hx as [r [Hr Hx]]; apply in_flat_map; exists r.
    split; auto; simpl; apply in_or_app; auto.
  - intros x Hx; apply in_flat_map in Hx as [o [Ho Hx]].
    destruct (opContribs_dom E pr c p o x Hx) as [y [Hy L]].
    exists y; split; auto; apply in_flat_map; eauto.
Qed.

(* ---- overlays only tighten ---- *)

Lemma overlay_fold_ge E pr c p s :
  rank (fold (map fst (contribs E c s))) <= rank (fold (map fst (contribs E (overlay pr c p) s))).
Proof. apply Dom_fold, contribs_dom. Qed.

Lemma overlay_wouldDeny E pr c p s :
  wouldDeny (evaluate E c s) = true -> wouldDeny (evaluate E (overlay pr c p) s) = true.
Proof.
  unfold evaluate, evalOf; simpl; rewrite !isDeny_true; intro H.
  pose proof (overlay_fold_ge E pr c p s) as L; rewrite H in L; now apply rank_top.
Qed.

(* "overlays only tighten policy" (layering_fuzz_test.go:13), for every
   subject and matcher: the final decision never goes down. *)
Lemma overlay_decision_ge E pr c p s :
  rank (decision_ (evaluate E c s)) <= rank (decision_ (evaluate E (overlay pr c p) s)).
Proof. apply finalize_mono; [apply overlayMode_ge | apply overlay_fold_ge]. Qed.

Lemma overlay_keeps_deny E pr c p s :
  decision_ (evaluate E c s) = Deny -> decision_ (evaluate E (overlay pr c p) s) = Deny.
Proof.
  intro H; pose proof (overlay_decision_ge E pr c p s) as L; rewrite H in L; now apply rank_top.
Qed.

(* user -> .ghok.yaml -> .ghok.local.yaml *)
Lemma overlay_chain_ge E pr pr' c p l s :
  rank (decision_ (evaluate E c s)) <=
    rank (decision_ (evaluate E (overlay pr' (overlay pr c p) l) s)).
Proof. eapply Nat.le_trans; apply overlay_decision_ge. Qed.
