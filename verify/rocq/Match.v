(* Model of the target part of internal/policy/match.go: opMatch (match.go:210),
   excluded (match.go:242) and the rule loop's use of them (evaluate.go:145).
   Glob matching stays abstract; non-target criteria are `other`. *)
From Stdlib Require Import List Bool String.
Import ListNotations.

Record target := { trepo : option string; thost : option string; repoLess : bool }.

Section Match.
Variable B : Type.
Variable glob : string -> string -> bool.

Record op := { base : B; t : target }.
Record m := { other : B -> bool; repos : list string; hosts : list string }.

(* None: no match; Some maybe *)
Definition crit (pats : list string) (v : option string) : option bool :=
  match pats with
  | [] => Some false
  | _ => match v with
         | None => Some true
         | Some r => if existsb (fun p => glob p r) pats then Some false else None
         end
  end.

Definition opMatch (x : m) (o : op) : option bool :=
  if negb (other x (base o)) then None
  else if negb (match repos x with [] => true | _ => false end) && repoLess (t o) then None
  else match crit (repos x) (trepo (t o)), crit (hosts x) (thost (t o)) with
       | Some a, Some b => Some (a || b)
       | _, _ => None
       end.

Definition isDefinite (r : option bool) : bool :=
  match r with Some false => true | _ => false end.

(* "Maybe-matches never exclude" (match.go:239) *)
Definition excluded (us : list m) (o : op) : bool := existsb (fun u => isDefinite (opMatch u o)) us.

Definition fires (x : m) (us : list m) (ignoreUnresolved : bool) (o : op) : bool :=
  match opMatch x o with
  | None => false
  | Some maybe => negb (maybe && ignoreUnresolved) && negb (excluded us o)
  end.

(* o' is o with (some of) its unresolved targets resolved *)
Record Refines (o o' : op) : Prop := {
  r_base : base o = base o';
  r_repoLess : repoLess (t o) = repoLess (t o');
  r_repo : trepo (t o) = None \/ trepo (t o) = trepo (t o');
  r_host : thost (t o) = None \/ thost (t o) = thost (t o') }.

Lemma crit_refine pats v v' b :
  (v = None \/ v = v') -> crit pats v' = Some b ->
  exists b', crit pats v = Some b' /\ (b = true -> b' = true).
Proof.
  intros [-> | ->] H; [|eauto].
  destruct pats; simpl in *; [|eauto].
  injection H as <-; eauto.
Qed.

Lemma crit_definite pats v v' :
  (v = None \/ v = v') -> crit pats v = Some false -> crit pats v' = Some false.
Proof. intros [-> | ->] H; [destruct pats; simpl in *; congruence | exact H]. Qed.

(* Resolving a target never turns a match into a non-match. *)
Lemma opMatch_refine x o o' b : Refines o o' -> opMatch x o' = Some b ->
  exists b', opMatch x o = Some b' /\ (b = true -> b' = true).
Proof.
  intros [Hb Hl Hr Hh]; unfold opMatch; rewrite Hb, Hl.
  destruct (negb _); [discriminate|].
  destruct (negb _ && _); [discriminate|].
  destruct (crit (repos x) (trepo (t o'))) as [a|] eqn:Ea; [|discriminate].
  destruct (crit (hosts x) (thost (t o'))) as [c|] eqn:Ec; [|discriminate].
  intro H; injection H as <-.
  destruct (crit_refine _ _ _ _ Hr Ea) as [a' [-> Ia]].
  destruct (crit_refine _ _ _ _ Hh Ec) as [c' [-> Ic]].
  exists (a' || c'); split; auto.
  intro H; apply orb_true_iff in H as [H|H]; apply orb_true_iff; auto.
Qed.

(* A definite match stays definite once targets resolve. *)
Lemma opMatch_definite x o o' : Refines o o' -> opMatch x o = Some false -> opMatch x o' = Some false.
Proof.
  intros [Hb Hl Hr Hh]; unfold opMatch; rewrite Hb, Hl.
  destruct (negb _); [discriminate|].
  destruct (negb _ && _); [discriminate|].
  destruct (crit (repos x) (trepo (t o))) as [a|] eqn:Ea; [|discriminate].
  destruct (crit (hosts x) (thost (t o))) as [c|] eqn:Ec; [|discriminate].
  intro H; injection H as H; apply orb_false_iff in H as [-> ->].
  rewrite (crit_definite _ _ _ Hr Ea), (crit_definite _ _ _ Hh Ec); reflexivity.
Qed.

(* Exclusion is stable under resolution. *)
Lemma excluded_refine us o o' : Refines o o' -> excluded us o = true -> excluded us o' = true.
Proof.
  intros R H; unfold excluded in *; apply existsb_exists in H as [u [Hu Hd]].
  apply existsb_exists; exists u; split; auto.
  destruct (opMatch u o) as [[|]|] eqn:E; try discriminate.
  now rewrite (opMatch_definite _ _ _ R E).
Qed.

(* Fail-closed matching: firing after resolution implies firing before,
   unless onUnresolved: ignore. Holds for every glob. *)
Lemma fires_conservative x us o o' : Refines o o' ->
  fires x us false o' = true -> fires x us false o = true.
Proof.
  intros R; unfold fires.
  destruct (opMatch x o') as [b|] eqn:E; [|discriminate].
  destruct (opMatch_refine _ _ _ _ R E) as [b' [-> _]].
  rewrite !andb_false_r; simpl; intro H.
  destruct (excluded us o) eqn:Ex; [|reflexivity].
  now rewrite (excluded_refine _ _ _ R Ex) in H.
Qed.

(* The opt-out is real: onUnresolved: ignore drops exactly the maybe-matches. *)
Lemma fires_ignore x us o :
  fires x us true o = true <-> opMatch x o = Some false /\ excluded us o = false.
Proof.
  unfold fires; destruct (opMatch x o) as [[|]|]; destruct (excluded us o); simpl;
    split; intro H; try discriminate; try (destruct H; discriminate); auto.
Qed.

End Match.
