(* Model of the decision side of internal/hooks/runner.go: effective
   (runner.go:72), classify (runner.go:92) and the aggregation loop
   (runner.go:280-298). The scheduler is out of model; every result, including
   the runner's diagnostics, goes through classify (runner.go:100-103). *)
From Stdlib Require Import List Bool Permutation.
Import ListNotations.

Inductive hdecision := HAbstain | HAllow | HDeny.
Inductive outcome := OK | Skipped | Failed.  (* Failed: timeout, canceled, invalid, exit, spawn *)
Inductive onError := OEAllow | OEIgnore | OEDeny.  (* Go's default branch: "", deny, ... *)
Inductive phase := Pre | Post.

Record hook := { hOnError : onError; continueOnError : bool }.
Record result := { routcome : outcome; rdecision : hdecision }.

Definition hdec_eq_dec (a b : hdecision) : {a = b} + {a <> b}.
Proof. decide equality. Defined.

(* runner.go:72 *)
Definition effective (ph : phase) (h : hook) (r : result) : hdecision :=
  match ph, routcome r with
  | Post, _ => HAbstain
  | Pre, OK => rdecision r
  | Pre, Skipped => HAbstain
  | Pre, Failed => match hOnError h with OEAllow => HAllow | OEIgnore => HAbstain | OEDeny => HDeny end
  end.

Definition isHDeny (d : hdecision) : bool := match d with HDeny => true | _ => false end.
Definition isPre (ph : phase) : bool := match ph with Pre => true | Post => false end.

(* runner.go:92: the effective decision and continuedOnError *)
Definition classify (ph : phase) (h : hook) (r : result) : hdecision * bool :=
  let e := effective ph h r in
  if isPre ph && continueOnError h && isHDeny e then (HAbstain, true) else (e, false).

(* runner.go:280-298, decision only *)
Fixpoint aggregate (ph : phase) (es : list hdecision) : hdecision :=
  match es with
  | [] => HAbstain
  | e :: t =>
    if isPre ph && isHDeny e then HDeny
    else let rest := aggregate ph t in
         match rest, e with HAbstain, HAllow => HAllow | _, _ => rest end
  end.

(* ---- effective / classify ---- *)

Lemma classify_post h r : fst (classify Post h r) = HAbstain.
Proof. reflexivity. Qed.

(* Fail-closed by default *)
Lemma classify_failed_deny h r :
  hOnError h = OEDeny -> continueOnError h = false -> routcome r = Failed ->
  fst (classify Pre h r) = HDeny.
Proof. intros Ho Hc Hr; unfold classify, effective; rewrite Hr, Ho, Hc; reflexivity. Qed.

Lemma classify_ok_deny h r :
  continueOnError h = false -> routcome r = OK -> rdecision r = HDeny ->
  fst (classify Pre h r) = HDeny.
Proof. intros Hc Hr Hd; unfold classify, effective; rewrite Hr, Hc, Hd; reflexivity. Qed.

Lemma classify_continue ph h r : continueOnError h = true -> fst (classify ph h r) <> HDeny.
Proof.
  intro Hc; unfold classify; rewrite Hc; destruct ph;
    [destruct (effective Pre h r)|]; simpl; congruence.
Qed.

Lemma classify_continued_iff ph h r :
  snd (classify ph h r) = true <-> ph = Pre /\ continueOnError h = true /\ effective ph h r = HDeny.
Proof.
  unfold classify; destruct ph, (continueOnError h), (effective _ h r); simpl;
    intuition congruence.
Qed.

Lemma classify_skipped ph h r : routcome r = Skipped -> fst (classify ph h r) = HAbstain.
Proof. intro Hr; destruct ph; unfold classify, effective; rewrite ?Hr; simpl; [destruct (continueOnError h)|]; reflexivity. Qed.

(* ---- aggregation ---- *)

(* What the loop computes, as a function of membership only. *)
Definition spec (ph : phase) (es : list hdecision) : hdecision :=
  if isPre ph && existsb isHDeny es then HDeny
  else if existsb (fun e => match e with HAllow => true | _ => false end) es then HAllow
  else HAbstain.

Lemma aggregate_eq_spec ph es : aggregate ph es = spec ph es.
Proof.
  induction es as [|e t IH]; [destruct ph; reflexivity|].
  simpl; rewrite IH; unfold spec.
  destruct ph, e; simpl;
    destruct (existsb isHDeny t), (existsb (fun e => match e with HAllow => true | _ => false end) t);
    reflexivity.
Qed.

Lemma aggregate_post_ne_deny es : aggregate Post es <> HDeny.
Proof. rewrite aggregate_eq_spec; unfold spec; simpl; destruct (existsb _ es); congruence. Qed.

(* No allow can override a pre denial. *)
Lemma aggregate_pre_deny_iff es : aggregate Pre es = HDeny <-> In HDeny es.
Proof.
  rewrite aggregate_eq_spec; unfold spec; simpl.
  destruct (existsb isHDeny es) eqn:E.
  - split; auto; intros _; apply existsb_exists in E as [x [Hx Hd]]; destruct x; try discriminate; auto.
  - split; [destruct (existsb (fun e => match e with HAllow => true | _ => false end) es);
      intro H; discriminate H|].
    intro H; assert (existsb isHDeny es = true) by (apply existsb_exists; eauto). congruence.
Qed.

Lemma existsb_perm {A} (f : A -> bool) xs ys : Permutation xs ys -> existsb f xs = existsb f ys.
Proof.
  intro P; apply eq_true_iff_eq; rewrite !existsb_exists.
  split; intros [x [Hx Hf]]; exists x; split; auto;
    [eapply Permutation_in | eapply Permutation_in; [symmetry|]]; eauto.
Qed.

(* Completion order does not matter. *)
Lemma aggregate_perm ph es es' : Permutation es es' -> aggregate ph es = aggregate ph es'.
Proof. intro P; rewrite !aggregate_eq_spec; unfold spec; rewrite !(existsb_perm _ _ _ P); reflexivity. Qed.

Lemma aggregate_pre_deny_mono es es' :
  (forall x, In x es -> In x es') -> aggregate Pre es = HDeny -> aggregate Pre es' = HDeny.
Proof. rewrite !aggregate_pre_deny_iff; auto. Qed.
