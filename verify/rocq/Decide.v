(* Model of internal/cli/decide_unix.go: evaluateConfig (trust-blocked gates on
   top of policy) and decide (policy + pre-hook aggregate -> verdict and exit
   code), called with cfg.Policy.Mode (wrapper_unix.go:332). *)
From Stdlib Require Import List Bool Arith Lia String.
From Ghok Require Import Policy Config Hooks.
Import ListNotations.

(* decide_unix.go:80-103. gate: some trust-blocked gate hook matches. *)
Definition evaluateConfig (E : env) (p : policy) (s : subject) (gate : bool) : evaluation :=
  let ev := evaluate E p s in
  if gate then {| decision_ := finalize (pmode p) Deny; wouldDeny := true;
                  denials := denials ev ++ [KConfig] |}
  else ev.

(* A blocked gate is just one more deny contribution, of kind config. *)
Lemma evaluateConfig_eq E p s gate :
  evaluateConfig E p s gate =
    evalOf (pmode p) (contribs E p s ++ if gate then [(Deny, KConfig)] else []).
Proof.
  unfold evaluateConfig, evaluate, evalOf; destruct gate; [|rewrite app_nil_r; reflexivity].
  rewrite map_app, fold_app, filter_app, map_app; simpl.
  rewrite merge_deny_r; reflexivity.
Qed.

Inductive verdict := VAllow | VAudit | VWouldDeny | VDeny.

Record hookOut := { eff : hdecision; ok : bool }.  (* ok: content vs error denial *)

Definition isKConfig (k : kind) : bool := match k with KConfig => true | _ => false end.
Definition isEnforce (m : mode) : bool := match m with Enforce => true | _ => false end.
Definition isAudit (d : decision) : bool := match d with Audit => true | _ => false end.

(* decide_unix.go:107-145. rulesEmpty: len(evaluation.Rules) == 0 *)
Definition decide (m : mode) (ev : evaluation) (pre : hdecision) (hooks : list hookOut)
    (stdinSkipped rulesEmpty : bool) : verdict * nat :=
  let wd := wouldDeny ev || isDeny (decision_ ev) || isHDeny pre in
  if wd then
    if isEnforce m then
      let content := existsb (fun k => negb (isKConfig k)) (denials ev) ||
                     existsb (fun h => isHDeny (eff h) && ok h) hooks in
      (VDeny, if content then 77 else 78)
    else (VWouldDeny, 0)
  else if stdinSkipped && rulesEmpty then (VAllow, 0)
  else if isAudit (decision_ ev) then (VAudit, 0)
  else (VAllow, 0).

Definition isOK (o : outcome) : bool := match o with OK => true | _ => false end.

Definition outs (hs : list (hook * result)) : list hookOut :=
  map (fun hr => {| eff := fst (classify Pre (fst hr) (snd hr)); ok := isOK (routcome (snd hr)) |}) hs.

Definition run E p s gate hs stdinSkipped rulesEmpty : verdict * nat :=
  decide (pmode p) (evaluateConfig E p s gate) (aggregate Pre (map eff (outs hs)))
    (outs hs) stdinSkipped rulesEmpty.

(* ---- lemmas ---- *)

Lemma ruleContribs_kind E s r c : In c (ruleContribs E s r) -> snd c = KRule.
Proof.
  unfold ruleContribs; intro H; apply in_app_or in H as [H|H].
  - destruct (noOps s && firesInv E r); [destruct H as [<-|[]]|destruct H]; reflexivity.
  - apply in_map_iff in H as [? [<- _]]; reflexivity.
Qed.

Lemma opContribs_kind E p o c : In c (opContribs E p o) -> snd c = KPolicy.
Proof.
  unfold opContribs; intro H; apply in_app_or in H as [H|H];
    repeat match type of H with
    | In _ (if ?b then _ else _) => destruct b
    | In _ [] => destruct H
    | In _ [_] => destruct H as [<-|[]]; reflexivity
    end.
Qed.

Lemma contribs_kind E p s c : In c (contribs E p s) -> snd c <> KConfig.
Proof.
  unfold contribs; intro H; apply in_app_or in H as [H|H]; apply in_flat_map in H as [x [_ H]].
  - rewrite (ruleContribs_kind _ _ _ _ H); discriminate.
  - rewrite (opContribs_kind _ _ _ _ H); discriminate.
Qed.

Lemma decision_deny_wouldDeny m cs :
  decision_ (evalOf m cs) = Deny -> wouldDeny (evalOf m cs) = true.
Proof. intro H; apply evalOf_decision_deny in H; tauto. Qed.

Definition PolicyDenies E p s gate : Prop :=
  (exists c, In c (contribs E p s) /\ fst c = Deny) \/ gate = true.

Definition HookDenies (hs : list (hook * result)) : Prop :=
  exists hr, In hr hs /\ fst (classify Pre (fst hr) (snd hr)) = HDeny.

Lemma evaluateConfig_wouldDeny E p s gate :
  wouldDeny (evaluateConfig E p s gate) = true <-> PolicyDenies E p s gate.
Proof.
  rewrite evaluateConfig_eq, evalOf_wouldDeny; unfold PolicyDenies.
  split.
  - intros [c [Hc Hd]]; apply in_app_or in Hc as [Hc|Hc]; [left; eauto|].
    destruct gate; [right; reflexivity|destruct Hc].
  - intros [[c [Hc Hd]] | ->]; [exists c; split; auto; apply in_or_app; auto|].
    exists (Deny, KConfig); split; auto; apply in_or_app; right; left; reflexivity.
Qed.

Lemma pre_deny_iff hs : aggregate Pre (map eff (outs hs)) = HDeny <-> HookDenies hs.
Proof.
  rewrite aggregate_pre_deny_iff; unfold HookDenies, outs; rewrite map_map; simpl.
  rewrite in_map_iff; split; intros [x [H1 H2]]; exists x; auto.
Qed.

Lemma wd_iff E p s gate hs :
  (wouldDeny (evaluateConfig E p s gate) || isDeny (decision_ (evaluateConfig E p s gate)) ||
   isHDeny (aggregate Pre (map eff (outs hs)))) = true <->
  PolicyDenies E p s gate \/ HookDenies hs.
Proof.
  rewrite <- evaluateConfig_wouldDeny, <- pre_deny_iff, !orb_true_iff, isDeny_true.
  assert (isHDeny (aggregate Pre (map eff (outs hs))) = true <->
          aggregate Pre (map eff (outs hs)) = HDeny) as ->
    by (destruct (aggregate _ _); simpl; split; congruence).
  rewrite evaluateConfig_eq, evalOf_decision_deny; tauto.
Qed.

(* ---- end-to-end ---- *)

Lemma run_deny_iff E p s gate hs ss re :
  fst (run E p s gate hs ss re) = VDeny <->
    pmode p = Enforce /\ (PolicyDenies E p s gate \/ HookDenies hs).
Proof.
  unfold run, decide.
  destruct (_ || _ || _) eqn:W.
  - apply wd_iff in W; destruct (pmode p); simpl; split; try discriminate; intuition congruence.
  - assert (~ (PolicyDenies E p s gate \/ HookDenies hs)) as N
      by (intro H; apply wd_iff in H; congruence).
    split; [|intuition].
    destruct (ss && re); [discriminate|]; destruct (isAudit _); discriminate.
Qed.

Lemma run_observe E p s gate hs ss re :
  pmode p = Observe -> fst (run E p s gate hs ss re) <> VDeny.
Proof. intros Hm H; apply run_deny_iff in H; rewrite Hm in H; destruct H; discriminate. Qed.

Lemma run_enforce_blocks E p s gate hs ss re :
  pmode p = Enforce -> PolicyDenies E p s gate \/ HookDenies hs ->
  fst (run E p s gate hs ss re) = VDeny.
Proof. intros; apply run_deny_iff; auto. Qed.

(* Fail-closed end to end *)
Lemma run_failed_hook_blocks E p s gate hs ss re h r :
  pmode p = Enforce -> In (h, r) hs -> hOnError h = OEDeny -> continueOnError h = false ->
  routcome r = Failed -> fst (run E p s gate hs ss re) = VDeny.
Proof.
  intros Hm Hin Ho Hc Hr; apply run_enforce_blocks; auto.
  right; exists (h, r); split; auto; now apply classify_failed_deny.
Qed.

(* Exit codes (docs/hook-protocol.md:364) *)
Lemma run_exit E p s gate hs ss re :
  fst (run E p s gate hs ss re) = VDeny ->
  (snd (run E p s gate hs ss re) = 77 <->
     (exists c, In c (contribs E p s) /\ fst c = Deny) \/
     (exists hr, In hr hs /\ fst (classify Pre (fst hr) (snd hr)) = HDeny /\ routcome (snd hr) = OK)) /\
  (snd (run E p s gate hs ss re) = 77 \/ snd (run E p s gate hs ss re) = 78).
Proof.
  unfold run, decide.
  destruct (_ || _ || _) eqn:W;
    [|destruct (ss && re); [discriminate|]; destruct (isAudit _); discriminate].
  destruct (isEnforce (pmode p)); [|discriminate]; intros _; simpl.
  split; [|match goal with |- (if ?b then _ else _) = _ \/ _ => destruct b; auto end].
  rewrite evaluateConfig_eq; unfold evalOf; simpl.
  assert (Content :
    (existsb (fun k => negb (isKConfig k))
       (map snd (filter (fun c => isDeny (fst c)) (contribs E p s ++ (if gate then [(Deny, KConfig)] else []))))
     || existsb (fun h => isHDeny (eff h) && ok h) (outs hs)) = true <->
    (exists c, In c (contribs E p s) /\ fst c = Deny) \/
    (exists hr, In hr hs /\ fst (classify Pre (fst hr) (snd hr)) = HDeny /\ routcome (snd hr) = OK)).
  { rewrite orb_true_iff, !existsb_exists. split.
    - intros [[k [Hk Hn]] | [o [Ho Hd]]].
      + apply in_map_iff in Hk as [c [<- Hc]]; apply filter_In in Hc as [Hc Hd].
        apply in_app_or in Hc as [Hc|Hc].
        * left; exists c; split; auto; now apply isDeny_true.
        * destruct gate; [destruct Hc as [<-|[]]; discriminate|destruct Hc].
      + right; unfold outs in Ho; apply in_map_iff in Ho as [hr [<- Hhr]].
        simpl in Hd; apply andb_true_iff in Hd as [Hd Hok].
        exists hr; split; auto; split.
        * revert Hd; destruct (fst (classify Pre (fst hr) (snd hr))); simpl; congruence.
        * revert Hok; destruct (routcome (snd hr)); simpl; congruence.
    - intros [[c [Hc Hd]] | [hr [Hhr [Hd Hok]]]].
      + left; exists (snd c); split.
        * apply in_map; apply filter_In; split; [apply in_or_app; auto|now apply isDeny_true].
        * pose proof (contribs_kind _ _ _ _ Hc); destruct (snd c); simpl; congruence.
      + right; exists {| eff := fst (classify Pre (fst hr) (snd hr)); ok := isOK (routcome (snd hr)) |}.
        split; [unfold outs; apply in_map_iff; eauto|simpl; rewrite Hd, Hok; reflexivity]. }
  match goal with |- (if ?b then _ else _) = _ <-> _ => destruct b eqn:C end.
  - split; [intros _; exact (proj1 Content eq_refl)|reflexivity].
  - split; [discriminate|intro H; apply (proj2 Content) in H; discriminate H].
Qed.

(* Overlays only tighten, end to end, provided the overlay's hook run keeps
   the user hooks' results (scheduler out of model) and blocked gates stay blocked. *)
Lemma run_overlay_keeps_deny E pr c p s gate gate' hs hs' ss re ss' re' :
  (gate = true -> gate' = true) -> (forall x, In x hs -> In x hs') ->
  fst (run E c s gate hs ss re) = VDeny -> fst (run E (overlay pr c p) s gate' hs' ss' re') = VDeny.
Proof.
  intros Hg Hh; rewrite !run_deny_iff; intros [Hm Hd]; split.
  - pose proof (overlayMode_ge (pMode pr) (pmode c) (pmode p)) as L; rewrite Hm in L.
    simpl; rewrite Hm; destruct (overlayMode (pMode pr) Enforce (pmode p)); simpl in L; [lia|reflexivity].
  - destruct Hd as [[[x [Hx Hxd]] | G] | [hr [Hin Hdeny]]].
    + destruct (contribs_dom E pr c p s x Hx) as [y [Hy L]]; rewrite Hxd in L.
      left; left; exists y; split; auto; now apply rank_top.
    + left; right; auto.
    + right; exists hr; auto.
Qed.
