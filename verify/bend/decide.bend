# Port of Ghok/Decide.lean: internal/cli/decide_unix.go; wrapper_unix.go:332.
# Prop exists/or/iff/membership are Bool any/all predicates and equalities.
# Records replace products. Field-equality hypotheses are normalized when noted.
# Base Empty is used for impossible Boolean hypotheses; no axioms or unsafe defs.
import Base
import ./policy.bend as P
import ./match.bend as M
import ./config.bend as C
import ./hooks.bend as H

type Verdict is Data:
  Allow{}
  Audit{}
  WouldDeny{}
  Deny{}
type Answer is Data:
  Answer{verdict: Verdict, code: Nat}
type HookOut is Data:
  HookOut{eff: H.HDecision, ok: Bool}
type HookRun is Data:
  HookRun{hook: H.Hook, result: H.HookResult}

def verdict(a: Answer) -> Verdict:
  Answer{v, n} = a
  v

def exit_code(a: Answer) -> Nat:
  Answer{v, n} = a
  n

def is_deny(v: Verdict) -> Bool:
  match v:
    case Deny{}:
      True{}
    case _:
      False{}

def is_audit(d: P.Decision) -> Bool:
  match d:
    case P.Audit{}:
      True{}
    case _:
      False{}

def is_config(k: P.Cause) -> Bool:
  match k:
    case P.ConfigCause{}:
      True{}
    case _:
      False{}

def kind_eq(a: P.Cause, b: P.Cause) -> Bool:
  match a b:
    case P.RuleCause{} P.RuleCause{}:
      True{}
    case P.PolicyCause{} P.PolicyCause{}:
      True{}
    case P.ConfigCause{} P.ConfigCause{}:
      True{}
    case _ _:
      False{}

# decide_unix.go:80-103: matching trust-blocked gate.
def gate_evaluation(+mode: P.Mode, +ev: P.Evaluation, gate: Bool) -> P.Evaluation:
  match gate:
    case False{}:
      ev
    case True{}:
      P.Evaluation{P.finalize(mode, P.Deny{}), True{}, P.kcat(P.ev_denials(ev), [P.ConfigCause{}])}

def evaluate_config(+e: P.Env, +p: P.Policy, +s: P.Subject, gate: Bool) -> P.Evaluation:
  gate_evaluation(P.policy_mode(p), P.evaluate(e, p, s), gate)

def denials_content(ks: +List<P.Cause>) -> Bool:
  match ks:
    case Nil{}:
      False{}
    case Con{k, t}:
      Bool.or(Bool.not(is_config(k)), denials_content(t))

def outs_content(hs: +List<HookOut>) -> Bool:
  match hs:
    case Nil{}:
      False{}
    case Con{HookOut{eff, ok}, t}:
      Bool.or(Bool.and(H.is_deny(eff), ok), outs_content(t))

def content(+ev: P.Evaluation, hooks: +List<HookOut>) -> Bool:
  Bool.or(denials_content(P.ev_denials(ev)), outs_content(hooks))

# decide_unix.go:107-145; denial is checked BEFORE the stdin relabel.
# ss=stdinSkipped; re=rulesEmpty, i.e. len(evaluation.Rules)==0.
def decide_bits(mode: P.Mode, wd: Bool, content: Bool, ss: Bool, re: Bool, audit: Bool) -> Answer:
  Bool.pick(Answer, wd,
    Bool.pick(Answer, P.is_enforce(mode), Answer{Deny{}, Bool.pick(Nat, content, 77n, 78n)}, Answer{WouldDeny{}, 0n}),
    Bool.pick(Answer, Bool.and(ss, re), Answer{Allow{}, 0n}, Bool.pick(Answer, audit, Answer{Audit{}, 0n}, Answer{Allow{}, 0n})))

def wd(+ev: P.Evaluation, pre: H.HDecision) -> Bool:
  Bool.or(Bool.or(P.ev_would_deny(ev), P.is_deny(P.ev_decision(ev))), H.is_deny(pre))

def decide(mode: P.Mode, +ev: P.Evaluation, pre: H.HDecision, hooks: +List<HookOut>, ss: Bool, re: Bool) -> Answer:
  decide_bits(mode, wd(ev, pre), content(ev, hooks), ss, re, is_audit(P.ev_decision(ev)))

def outs(hs: +List<HookRun>) -> +List<HookOut>:
  match hs:
    case Nil{}:
      []
    case Con{HookRun{h, +r}, t}:
      HookOut{H.eff(H.classify(H.Pre{}, h, r)), H.is_ok(r)} <> outs(t)

def pre_decisions(os: +List<HookOut>) -> +List<H.HDecision>:
  match os:
    case Nil{}:
      []
    case Con{HookOut{d, ok}, t}:
      d <> pre_decisions(t)

def pre(hs: +List<HookRun>) -> H.HDecision:
  H.aggregate(H.Pre{}, pre_decisions(outs(hs)))

# Whole pre-gh pipeline, with the same cfg.Policy.Mode as Evaluate.
def run(+e: P.Env, +p: P.Policy, +s: P.Subject, gate: Bool, +hs: +List<HookRun>, ss: Bool, re: Bool) -> Answer:
  decide(P.policy_mode(p), evaluate_config(e, p, s, gate), pre(hs), outs(hs), ss, re)

def PolicyDenies(+e: P.Env, +p: P.Policy, +s: P.Subject, gate: Bool) -> Bool:
  Bool.or(P.any_deny(P.contribs(e, p, s)), gate)

def HookDenies(hs: +List<HookRun>) -> Bool:
  match hs:
    case Nil{}:
      False{}
    case Con{HookRun{h, r}, t}:
      Bool.or(H.is_deny(H.eff(H.classify(H.Pre{}, h, r))), HookDenies(t))

def hook_content(hs: +List<HookRun>) -> Bool:
  match hs:
    case Nil{}:
      False{}
    case Con{HookRun{h, +r}, t}:
      Bool.or(Bool.and(H.is_deny(H.eff(H.classify(H.Pre{}, h, r))), H.is_ok(r)), hook_content(t))

law or_false_r:
  for +a: Bool
  {Bool.or(a, False{}) == a : Bool}

def or_false_r(a):
  match a:
    case False{}:
      {==}
    case True{}:
      {==}

law gate_wd_bits:
  for +a: Bool
  for +g: Bool
  {Bool.pick(Bool, g, True{}, a) == Bool.or(a, g) : Bool}

def gate_wd_bits(a, g):
  match a g:
    case False{} False{}:
      {==}
    case False{} True{}:
      {==}
    case True{} False{}:
      {==}
    case True{} True{}:
      {==}

law gate_wd:
  for +m: P.Mode
  for +ev: P.Evaluation
  for +g: Bool
  {P.ev_would_deny(gate_evaluation(m, ev, g)) == Bool.or(P.ev_would_deny(ev), g) : Bool}
def gate_wd(m, ev, g):
  match ev g:
    case P.Evaluation{d, +a, ds} False{}:
      Equal.sym(Bool, Bool.or(a, False{}), a, or_false_r(a))
    case P.Evaluation{d, False{}, ds} True{}:
      {==}
    case P.Evaluation{d, True{}, ds} True{}:
      {==}

law evaluateConfig_wouldDeny:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  {P.ev_would_deny(evaluate_config(e, p, s, g)) == PolicyDenies(e, p, s, g) : Bool}
def evaluateConfig_wouldDeny(e, p, s, g):
  %P.evalOf_wouldDeny(P.policy_mode(p), P.contribs(e, p, s)) : {P.ev_would_deny(evaluate_config(e, p, s, g)) == Bool.or(_, g) : Bool}
  gate_wd(P.policy_mode(p), P.evaluate(e, p, s), g)

law outs_deny:
  for +hs: +List<HookRun>
  {H.has_deny(pre_decisions(outs(hs))) == HookDenies(hs) : Bool}
def outs_deny(hs):
  match hs:
    case Nil{}:
      {==}
    case Con{HookRun{+h, +r}, +t}:
      %outs_deny(t) : {Bool.or(H.is_deny(H.eff(H.classify(H.Pre{}, h, r))), H.has_deny(pre_decisions(outs(t)))) == Bool.or(H.is_deny(H.eff(H.classify(H.Pre{}, h, r))), _) : Bool}
      {==}

law pre_deny_iff:
  for +hs: +List<HookRun>
  {H.is_deny(pre(hs)) == HookDenies(hs) : Bool}
def pre_deny_iff(hs):
  Equal.trans(Bool, H.is_deny(pre(hs)), H.has_deny(pre_decisions(outs(hs))), HookDenies(hs), H.aggregate_pre_deny_iff(pre_decisions(outs(hs))), outs_deny(hs))

law and_implies_right:
  for +a: Bool
  for +b: Bool
  {P.implies(Bool.and(a, b), b) == True{} : Bool}

def and_implies_right(a, b):
  match a b:
    case False{} False{}:
      {==}
    case False{} True{}:
      {==}
    case True{} False{}:
      {==}
    case True{} True{}:
      {==}

law decision_deny_wouldDeny:
  for +m: P.Mode
  for +cs: +List<P.Contrib>
  {P.implies(P.is_deny(P.ev_decision(P.eval_of(m, cs))), P.ev_would_deny(P.eval_of(m, cs))) == True{} : Bool}
def decision_deny_wouldDeny(m, cs):
  %Equal.sym(Bool, P.is_deny(P.ev_decision(P.eval_of(m, cs))), Bool.and(P.is_enforce(m), P.ev_would_deny(P.eval_of(m, cs))), P.evalOf_decision_deny(m, cs)) : {P.implies(_, P.ev_would_deny(P.eval_of(m, cs))) == True{} : Bool}
  and_implies_right(P.is_enforce(m), P.ev_would_deny(P.eval_of(m, cs)))

def gate_decision_wd(+m: P.Mode, +d: P.Decision, +a: Bool, +g: Bool, ks: +List<P.Cause>, h: {P.implies(P.is_deny(d), a) == True{} : Bool}) -> {P.implies(P.is_deny(P.ev_decision(gate_evaluation(m, P.Evaluation{d, a, ks}, g))), P.ev_would_deny(gate_evaluation(m, P.Evaluation{d, a, ks}, g))) == True{} : Bool}:
  match m d a g:
    case P.Observe{} P.Allow{} False{} False{}:
      {==}
    case P.Observe{} P.Allow{} False{} True{}:
      {==}
    case P.Observe{} P.Allow{} True{} False{}:
      {==}
    case P.Observe{} P.Allow{} True{} True{}:
      {==}
    case P.Observe{} P.Audit{} False{} False{}:
      {==}
    case P.Observe{} P.Audit{} False{} True{}:
      {==}
    case P.Observe{} P.Audit{} True{} False{}:
      {==}
    case P.Observe{} P.Audit{} True{} True{}:
      {==}
    case P.Observe{} P.Deny{} False{} False{}:
      P.absurd({P.implies(P.is_deny(P.ev_decision(gate_evaluation(P.Observe{}, P.Evaluation{P.Deny{}, False{}, ks}, False{}))), P.ev_would_deny(gate_evaluation(P.Observe{}, P.Evaluation{P.Deny{}, False{}, ks}, False{}))) == True{} : Bool}, h)
    case P.Observe{} P.Deny{} False{} True{}:
      {==}
    case P.Observe{} P.Deny{} True{} False{}:
      {==}
    case P.Observe{} P.Deny{} True{} True{}:
      {==}
    case P.Enforce{} P.Allow{} False{} False{}:
      {==}
    case P.Enforce{} P.Allow{} False{} True{}:
      {==}
    case P.Enforce{} P.Allow{} True{} False{}:
      {==}
    case P.Enforce{} P.Allow{} True{} True{}:
      {==}
    case P.Enforce{} P.Audit{} False{} False{}:
      {==}
    case P.Enforce{} P.Audit{} False{} True{}:
      {==}
    case P.Enforce{} P.Audit{} True{} False{}:
      {==}
    case P.Enforce{} P.Audit{} True{} True{}:
      {==}
    case P.Enforce{} P.Deny{} False{} False{}:
      P.absurd({P.implies(P.is_deny(P.ev_decision(gate_evaluation(P.Enforce{}, P.Evaluation{P.Deny{}, False{}, ks}, False{}))), P.ev_would_deny(gate_evaluation(P.Enforce{}, P.Evaluation{P.Deny{}, False{}, ks}, False{}))) == True{} : Bool}, h)
    case P.Enforce{} P.Deny{} False{} True{}:
      {==}
    case P.Enforce{} P.Deny{} True{} False{}:
      {==}
    case P.Enforce{} P.Deny{} True{} True{}:
      {==}

def evaluate_config_decision_wd(+e: P.Env, +p: P.Policy, +s: P.Subject, +g: Bool) -> {P.implies(P.is_deny(P.ev_decision(evaluate_config(e, p, s, g))), P.ev_would_deny(evaluate_config(e, p, s, g))) == True{} : Bool}:
  gate_decision_wd(P.policy_mode(p), P.ev_decision(P.evaluate(e, p, s)), P.ev_would_deny(P.evaluate(e, p, s)), g, P.ev_denials(P.evaluate(e, p, s)), decision_deny_wouldDeny(P.policy_mode(p), P.contribs(e, p, s)))

def wd_bits(+a: Bool, +b: Bool, +c: Bool, h: {P.implies(b, a) == True{} : Bool}) -> {Bool.or(Bool.or(a, b), c) == Bool.or(a, c) : Bool}:
  match a b c:
    case False{} False{} False{}:
      {==}
    case False{} False{} True{}:
      {==}
    case False{} True{} False{}:
      P.absurd({Bool.or(Bool.or(False{}, True{}), False{}) == Bool.or(False{}, False{}) : Bool}, h)
    case False{} True{} True{}:
      {==}
    case True{} False{} False{}:
      {==}
    case True{} False{} True{}:
      {==}
    case True{} True{} False{}:
      {==}
    case True{} True{} True{}:
      {==}

law wd_iff:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  {wd(evaluate_config(e, p, s, g), pre(hs)) == Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs)) : Bool}
def wd_iff(e, p, s, g, hs):
  %evaluateConfig_wouldDeny(e, p, s, g) : {wd(evaluate_config(e, p, s, g), pre(hs)) == Bool.or(_, HookDenies(hs)) : Bool}
  %pre_deny_iff(hs) : {wd(evaluate_config(e, p, s, g), pre(hs)) == Bool.or(P.ev_would_deny(evaluate_config(e, p, s, g)), _) : Bool}
  wd_bits(P.ev_would_deny(evaluate_config(e, p, s, g)), P.is_deny(P.ev_decision(evaluate_config(e, p, s, g))), H.is_deny(pre(hs)), evaluate_config_decision_wd(e, p, s, g))

law decide_deny_bits:
  for +m: P.Mode
  for +wd: Bool
  for +content: Bool
  for +ss: Bool
  for +re: Bool
  for +audit: Bool
  {is_deny(verdict(decide_bits(m, wd, content, ss, re, audit))) == Bool.and(P.is_enforce(m), wd) : Bool}

def decide_deny_bits(m, wd, content, ss, re, audit):
  match m wd content ss re audit:
    case P.Observe{} False{} False{} False{} False{} False{}:
      {==}
    case P.Observe{} False{} False{} False{} False{} True{}:
      {==}
    case P.Observe{} False{} False{} False{} True{} False{}:
      {==}
    case P.Observe{} False{} False{} False{} True{} True{}:
      {==}
    case P.Observe{} False{} False{} True{} False{} False{}:
      {==}
    case P.Observe{} False{} False{} True{} False{} True{}:
      {==}
    case P.Observe{} False{} False{} True{} True{} False{}:
      {==}
    case P.Observe{} False{} False{} True{} True{} True{}:
      {==}
    case P.Observe{} False{} True{} False{} False{} False{}:
      {==}
    case P.Observe{} False{} True{} False{} False{} True{}:
      {==}
    case P.Observe{} False{} True{} False{} True{} False{}:
      {==}
    case P.Observe{} False{} True{} False{} True{} True{}:
      {==}
    case P.Observe{} False{} True{} True{} False{} False{}:
      {==}
    case P.Observe{} False{} True{} True{} False{} True{}:
      {==}
    case P.Observe{} False{} True{} True{} True{} False{}:
      {==}
    case P.Observe{} False{} True{} True{} True{} True{}:
      {==}
    case P.Observe{} True{} False{} False{} False{} False{}:
      {==}
    case P.Observe{} True{} False{} False{} False{} True{}:
      {==}
    case P.Observe{} True{} False{} False{} True{} False{}:
      {==}
    case P.Observe{} True{} False{} False{} True{} True{}:
      {==}
    case P.Observe{} True{} False{} True{} False{} False{}:
      {==}
    case P.Observe{} True{} False{} True{} False{} True{}:
      {==}
    case P.Observe{} True{} False{} True{} True{} False{}:
      {==}
    case P.Observe{} True{} False{} True{} True{} True{}:
      {==}
    case P.Observe{} True{} True{} False{} False{} False{}:
      {==}
    case P.Observe{} True{} True{} False{} False{} True{}:
      {==}
    case P.Observe{} True{} True{} False{} True{} False{}:
      {==}
    case P.Observe{} True{} True{} False{} True{} True{}:
      {==}
    case P.Observe{} True{} True{} True{} False{} False{}:
      {==}
    case P.Observe{} True{} True{} True{} False{} True{}:
      {==}
    case P.Observe{} True{} True{} True{} True{} False{}:
      {==}
    case P.Observe{} True{} True{} True{} True{} True{}:
      {==}
    case P.Enforce{} False{} False{} False{} False{} False{}:
      {==}
    case P.Enforce{} False{} False{} False{} False{} True{}:
      {==}
    case P.Enforce{} False{} False{} False{} True{} False{}:
      {==}
    case P.Enforce{} False{} False{} False{} True{} True{}:
      {==}
    case P.Enforce{} False{} False{} True{} False{} False{}:
      {==}
    case P.Enforce{} False{} False{} True{} False{} True{}:
      {==}
    case P.Enforce{} False{} False{} True{} True{} False{}:
      {==}
    case P.Enforce{} False{} False{} True{} True{} True{}:
      {==}
    case P.Enforce{} False{} True{} False{} False{} False{}:
      {==}
    case P.Enforce{} False{} True{} False{} False{} True{}:
      {==}
    case P.Enforce{} False{} True{} False{} True{} False{}:
      {==}
    case P.Enforce{} False{} True{} False{} True{} True{}:
      {==}
    case P.Enforce{} False{} True{} True{} False{} False{}:
      {==}
    case P.Enforce{} False{} True{} True{} False{} True{}:
      {==}
    case P.Enforce{} False{} True{} True{} True{} False{}:
      {==}
    case P.Enforce{} False{} True{} True{} True{} True{}:
      {==}
    case P.Enforce{} True{} False{} False{} False{} False{}:
      {==}
    case P.Enforce{} True{} False{} False{} False{} True{}:
      {==}
    case P.Enforce{} True{} False{} False{} True{} False{}:
      {==}
    case P.Enforce{} True{} False{} False{} True{} True{}:
      {==}
    case P.Enforce{} True{} False{} True{} False{} False{}:
      {==}
    case P.Enforce{} True{} False{} True{} False{} True{}:
      {==}
    case P.Enforce{} True{} False{} True{} True{} False{}:
      {==}
    case P.Enforce{} True{} False{} True{} True{} True{}:
      {==}
    case P.Enforce{} True{} True{} False{} False{} False{}:
      {==}
    case P.Enforce{} True{} True{} False{} False{} True{}:
      {==}
    case P.Enforce{} True{} True{} False{} True{} False{}:
      {==}
    case P.Enforce{} True{} True{} False{} True{} True{}:
      {==}
    case P.Enforce{} True{} True{} True{} False{} False{}:
      {==}
    case P.Enforce{} True{} True{} True{} False{} True{}:
      {==}
    case P.Enforce{} True{} True{} True{} True{} False{}:
      {==}
    case P.Enforce{} True{} True{} True{} True{} True{}:
      {==}

law decide_exit_bits:
  for +m: P.Mode
  for +wd: Bool
  for +content: Bool
  for +ss: Bool
  for +re: Bool
  for +audit: Bool
  {exit_code(decide_bits(m, wd, content, ss, re, audit)) == Bool.pick(Nat, is_deny(verdict(decide_bits(m, wd, content, ss, re, audit))), Bool.pick(Nat, content, 77n, 78n), 0n) : Nat}

def decide_exit_bits(m, wd, content, ss, re, audit):
  match m wd content ss re audit:
    case P.Observe{} False{} False{} False{} False{} False{}:
      {==}
    case P.Observe{} False{} False{} False{} False{} True{}:
      {==}
    case P.Observe{} False{} False{} False{} True{} False{}:
      {==}
    case P.Observe{} False{} False{} False{} True{} True{}:
      {==}
    case P.Observe{} False{} False{} True{} False{} False{}:
      {==}
    case P.Observe{} False{} False{} True{} False{} True{}:
      {==}
    case P.Observe{} False{} False{} True{} True{} False{}:
      {==}
    case P.Observe{} False{} False{} True{} True{} True{}:
      {==}
    case P.Observe{} False{} True{} False{} False{} False{}:
      {==}
    case P.Observe{} False{} True{} False{} False{} True{}:
      {==}
    case P.Observe{} False{} True{} False{} True{} False{}:
      {==}
    case P.Observe{} False{} True{} False{} True{} True{}:
      {==}
    case P.Observe{} False{} True{} True{} False{} False{}:
      {==}
    case P.Observe{} False{} True{} True{} False{} True{}:
      {==}
    case P.Observe{} False{} True{} True{} True{} False{}:
      {==}
    case P.Observe{} False{} True{} True{} True{} True{}:
      {==}
    case P.Observe{} True{} False{} False{} False{} False{}:
      {==}
    case P.Observe{} True{} False{} False{} False{} True{}:
      {==}
    case P.Observe{} True{} False{} False{} True{} False{}:
      {==}
    case P.Observe{} True{} False{} False{} True{} True{}:
      {==}
    case P.Observe{} True{} False{} True{} False{} False{}:
      {==}
    case P.Observe{} True{} False{} True{} False{} True{}:
      {==}
    case P.Observe{} True{} False{} True{} True{} False{}:
      {==}
    case P.Observe{} True{} False{} True{} True{} True{}:
      {==}
    case P.Observe{} True{} True{} False{} False{} False{}:
      {==}
    case P.Observe{} True{} True{} False{} False{} True{}:
      {==}
    case P.Observe{} True{} True{} False{} True{} False{}:
      {==}
    case P.Observe{} True{} True{} False{} True{} True{}:
      {==}
    case P.Observe{} True{} True{} True{} False{} False{}:
      {==}
    case P.Observe{} True{} True{} True{} False{} True{}:
      {==}
    case P.Observe{} True{} True{} True{} True{} False{}:
      {==}
    case P.Observe{} True{} True{} True{} True{} True{}:
      {==}
    case P.Enforce{} False{} False{} False{} False{} False{}:
      {==}
    case P.Enforce{} False{} False{} False{} False{} True{}:
      {==}
    case P.Enforce{} False{} False{} False{} True{} False{}:
      {==}
    case P.Enforce{} False{} False{} False{} True{} True{}:
      {==}
    case P.Enforce{} False{} False{} True{} False{} False{}:
      {==}
    case P.Enforce{} False{} False{} True{} False{} True{}:
      {==}
    case P.Enforce{} False{} False{} True{} True{} False{}:
      {==}
    case P.Enforce{} False{} False{} True{} True{} True{}:
      {==}
    case P.Enforce{} False{} True{} False{} False{} False{}:
      {==}
    case P.Enforce{} False{} True{} False{} False{} True{}:
      {==}
    case P.Enforce{} False{} True{} False{} True{} False{}:
      {==}
    case P.Enforce{} False{} True{} False{} True{} True{}:
      {==}
    case P.Enforce{} False{} True{} True{} False{} False{}:
      {==}
    case P.Enforce{} False{} True{} True{} False{} True{}:
      {==}
    case P.Enforce{} False{} True{} True{} True{} False{}:
      {==}
    case P.Enforce{} False{} True{} True{} True{} True{}:
      {==}
    case P.Enforce{} True{} False{} False{} False{} False{}:
      {==}
    case P.Enforce{} True{} False{} False{} False{} True{}:
      {==}
    case P.Enforce{} True{} False{} False{} True{} False{}:
      {==}
    case P.Enforce{} True{} False{} False{} True{} True{}:
      {==}
    case P.Enforce{} True{} False{} True{} False{} False{}:
      {==}
    case P.Enforce{} True{} False{} True{} False{} True{}:
      {==}
    case P.Enforce{} True{} False{} True{} True{} False{}:
      {==}
    case P.Enforce{} True{} False{} True{} True{} True{}:
      {==}
    case P.Enforce{} True{} True{} False{} False{} False{}:
      {==}
    case P.Enforce{} True{} True{} False{} False{} True{}:
      {==}
    case P.Enforce{} True{} True{} False{} True{} False{}:
      {==}
    case P.Enforce{} True{} True{} False{} True{} True{}:
      {==}
    case P.Enforce{} True{} True{} True{} False{} False{}:
      {==}
    case P.Enforce{} True{} True{} True{} False{} True{}:
      {==}
    case P.Enforce{} True{} True{} True{} True{} False{}:
      {==}
    case P.Enforce{} True{} True{} True{} True{} True{}:
      {==}

law run_deny_iff:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  {is_deny(verdict(run(e, p, s, g, hs, ss, re))) == Bool.and(P.is_enforce(P.policy_mode(p)), Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs))) : Bool}
def run_deny_iff(e, p, s, g, hs, ss, re):
  %wd_iff(e, p, s, g, hs) : {is_deny(verdict(run(e, p, s, g, hs, ss, re))) == Bool.and(P.is_enforce(P.policy_mode(p)), _) : Bool}
  decide_deny_bits(P.policy_mode(p), wd(evaluate_config(e, p, s, g), pre(hs)), content(evaluate_config(e, p, s, g), outs(hs)), ss, re, is_audit(P.ev_decision(evaluate_config(e, p, s, g))))

law run_observe:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  for hm: {P.policy_mode(p) == P.Observe{} : P.Mode}
  {is_deny(verdict(run(e, p, s, g, hs, ss, re))) == False{} : Bool}
def run_observe(e, p, s, g, hs, ss, re, hm):
  %Equal.sym(Bool, is_deny(verdict(run(e, p, s, g, hs, ss, re))), Bool.and(P.is_enforce(P.policy_mode(p)), Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs))), run_deny_iff(e, p, s, g, hs, ss, re)) : {_ == False{} : Bool}
  %Equal.sym(P.Mode, P.policy_mode(p), P.Observe{}, hm) : {Bool.and(P.is_enforce(_), Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs))) == False{} : Bool}
  {==}

law run_enforce_blocks:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  for hm: {P.policy_mode(p) == P.Enforce{} : P.Mode}
  for h: {Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs)) == True{} : Bool}
  {is_deny(verdict(run(e, p, s, g, hs, ss, re))) == True{} : Bool}
def run_enforce_blocks(e, p, s, g, hs, ss, re, hm, h):
  %Equal.sym(Bool, is_deny(verdict(run(e, p, s, g, hs, ss, re))), Bool.and(P.is_enforce(P.policy_mode(p)), Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs))), run_deny_iff(e, p, s, g, hs, ss, re)) : {_ == True{} : Bool}
  %Equal.sym(P.Mode, P.policy_mode(p), P.Enforce{}, hm) : {Bool.and(P.is_enforce(_), Bool.or(PolicyDenies(e, p, s, g), HookDenies(hs))) == True{} : Bool}
  h

# Normalize the witness h,r plus membership/equalities in Lean to an any-predicate
# finding precisely a failed/default-deny/non-continuing hook in the input list.
def failed_default(hr: HookRun) -> Bool:
  match hr:
    case HookRun{H.Hook{H.ErrorDeny{}, False{}}, H.HookResult{H.Failed{}, d}}:
      True{}
    case _:
      False{}

def failed_default_in(hs: +List<HookRun>) -> Bool:
  match hs:
    case Nil{}:
      False{}
    case Con{hr, t}:
      Bool.or(failed_default(hr), failed_default_in(t))

def hr_denies(hr: HookRun) -> Bool:
  HookRun{h, r} = hr
  H.is_deny(H.eff(H.classify(H.Pre{}, h, r)))

def failed_default_deny(+hr: HookRun) -> {P.implies(failed_default(hr), hr_denies(hr)) == True{} : Bool}:
  match hr:
    case HookRun{H.Hook{H.ErrorAllow{}, c}, r}:
      {==}
    case HookRun{H.Hook{H.Ignore{}, c}, r}:
      {==}
    case HookRun{H.Hook{H.ErrorDeny{}, True{}}, r}:
      {==}
    case HookRun{H.Hook{H.ErrorDeny{}, False{}}, H.HookResult{H.Ok{}, d}}:
      {==}
    case HookRun{H.Hook{H.ErrorDeny{}, False{}}, H.HookResult{H.Skipped{}, d}}:
      {==}
    case HookRun{H.Hook{H.ErrorDeny{}, False{}}, H.HookResult{H.Failed{}, d}}:
      {==}

law failed_list_denies:
  for +hs: +List<HookRun>
  {P.implies(failed_default_in(hs), HookDenies(hs)) == True{} : Bool}
def failed_list_denies(hs):
  match hs:
    case Nil{}:
      {==}
    case Con{HookRun{+h, +r}, +t}:
      M.or_mono(failed_default(HookRun{h, r}), hr_denies(HookRun{h, r}), failed_default_in(t), HookDenies(t), failed_default_deny(HookRun{h, r}), failed_list_denies(t))

law run_failed_hook_blocks:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  for hm: {P.policy_mode(p) == P.Enforce{} : P.Mode}
  for h: {failed_default_in(hs) == True{} : Bool}
  {is_deny(verdict(run(e, p, s, g, hs, ss, re))) == True{} : Bool}
def run_failed_hook_blocks(e, p, s, g, hs, ss, re, hm, h):
  run_enforce_blocks(e, p, s, g, hs, ss, re, hm, C.or_right(PolicyDenies(e, p, s, g), HookDenies(hs), P.mp(failed_default_in(hs), HookDenies(hs), failed_list_denies(hs), h)))

def all_kind(cs: +List<P.Contrib>, +k: P.Cause) -> Bool:
  match cs:
    case Nil{}:
      True{}
    case Con{P.Contrib{d, kk}, t}:
      Bool.and(kind_eq(kk, k), all_kind(t, k))

def no_config(cs: +List<P.Contrib>) -> Bool:
  match cs:
    case Nil{}:
      True{}
    case Con{P.Contrib{d, k}, t}:
      Bool.and(Bool.not(is_config(k)), no_config(t))

law all_kind_cat:
  for +xs: +List<P.Contrib>
  for +ys: +List<P.Contrib>
  for +k: P.Cause
  {all_kind(P.ccat(xs, ys), k) == Bool.and(all_kind(xs, k), all_kind(ys, k)) : Bool}
def all_kind_cat(xs, ys, k):
  match xs:
    case Nil{}:
      {==}
    case Con{P.Contrib{d, +kk}, +t}:
      %Equal.sym(Bool, all_kind(P.ccat(t, ys), k), Bool.and(all_kind(t, k), all_kind(ys, k)), all_kind_cat(t, ys, k)) : {Bool.and(kind_eq(kk, k), _) == Bool.and(all_kind(P.Contrib{d, kk} <> t, k), all_kind(ys, k)) : Bool}
      C.and_assoc(kind_eq(kk, k), all_kind(t, k), all_kind(ys, k))

law no_config_cat:
  for +xs: +List<P.Contrib>
  for +ys: +List<P.Contrib>
  {no_config(P.ccat(xs, ys)) == Bool.and(no_config(xs), no_config(ys)) : Bool}
def no_config_cat(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{P.Contrib{d, +k}, +t}:
      %Equal.sym(Bool, no_config(P.ccat(t, ys)), Bool.and(no_config(t), no_config(ys)), no_config_cat(t, ys)) : {Bool.and(Bool.not(is_config(k)), _) == Bool.and(no_config(P.Contrib{d, k} <> t), no_config(ys)) : Bool}
      C.and_assoc(Bool.not(is_config(k)), no_config(t), no_config(ys))

def one_rule_kind(+b: Bool, +d: P.Decision) -> {all_kind(P.one_if(b, d, P.RuleCause{}), P.RuleCause{}) == True{} : Bool}:
  match b:
    case False{}:
      {==}
    case True{}:
      {==}

law rule_ops_kind:
  for +os: +List<P.Op>
  for +e: P.Env
  for +r: P.Rule
  {all_kind(P.rule_ops(os, e, r), P.RuleCause{}) == True{} : Bool}
def rule_ops_kind(os, e, r):
  match os r:
    case Nil{} r:
      {==}
    case Con{+o, +t} P.Rule{+id, +d}:
      Equal.trans(Bool, all_kind(P.rule_ops(o <> t, e, P.Rule{id, d}), P.RuleCause{}), Bool.and(all_kind(P.one_if(P.fires(e, P.Rule{id, d}, o), d, P.RuleCause{}), P.RuleCause{}), all_kind(P.rule_ops(t, e, P.Rule{id, d}), P.RuleCause{})), True{}, all_kind_cat(P.one_if(P.fires(e, P.Rule{id, d}, o), d, P.RuleCause{}), P.rule_ops(t, e, P.Rule{id, d}), P.RuleCause{}), C.and_intro(all_kind(P.one_if(P.fires(e, P.Rule{id, d}, o), d, P.RuleCause{}), P.RuleCause{}), all_kind(P.rule_ops(t, e, P.Rule{id, d}), P.RuleCause{}), one_rule_kind(P.fires(e, P.Rule{id, d}, o), d), rule_ops_kind(t, e, P.Rule{id, d})))

# Universal kind property is equivalent to Lean's member -> kind statement.
law ruleContribs_kind:
  for +e: P.Env
  for +s: P.Subject
  for +r: P.Rule
  {all_kind(P.rule_contribs(e, s, r), P.RuleCause{}) == True{} : Bool}
def ruleContribs_kind(e, s, r):
  match s r:
    case P.Subject{+os, +no} P.Rule{+id, +d}:
      Equal.trans(Bool, all_kind(P.rule_contribs(e, P.Subject{os, no}, P.Rule{id, d}), P.RuleCause{}), Bool.and(all_kind(P.one_if(Bool.and(no, P.fires_inv(e, P.Rule{id, d})), d, P.RuleCause{}), P.RuleCause{}), all_kind(P.rule_ops(os, e, P.Rule{id, d}), P.RuleCause{})), True{}, all_kind_cat(P.one_if(Bool.and(no, P.fires_inv(e, P.Rule{id, d})), d, P.RuleCause{}), P.rule_ops(os, e, P.Rule{id, d}), P.RuleCause{}), C.and_intro(all_kind(P.one_if(Bool.and(no, P.fires_inv(e, P.Rule{id, d})), d, P.RuleCause{}), P.RuleCause{}), all_kind(P.rule_ops(os, e, P.Rule{id, d}), P.RuleCause{}), one_rule_kind(Bool.and(no, P.fires_inv(e, P.Rule{id, d})), d), rule_ops_kind(os, e, P.Rule{id, d})))

def op_kind_bits(+i: Bool, +u: Bool, +blocked: Bool, +unresolved: Bool, +protected: Bool, +ui: P.Decision, +um: P.Decision, +pu: P.Decision) -> {all_kind(P.ccat(Bool.pick(+List<P.Contrib>, i, [P.Contrib{ui, P.PolicyCause{}}], P.one_if(u, um, P.PolicyCause{})), Bool.pick(+List<P.Contrib>, blocked, [], Bool.pick(+List<P.Contrib>, unresolved, [P.Contrib{pu, P.PolicyCause{}}], P.one_if(protected, pu, P.PolicyCause{})))), P.PolicyCause{}) == True{} : Bool}:
  match i u blocked unresolved protected:
    case False{} False{} False{} False{} False{}:
      {==}
    case False{} False{} False{} False{} True{}:
      {==}
    case False{} False{} False{} True{} False{}:
      {==}
    case False{} False{} False{} True{} True{}:
      {==}
    case False{} False{} True{} False{} False{}:
      {==}
    case False{} False{} True{} False{} True{}:
      {==}
    case False{} False{} True{} True{} False{}:
      {==}
    case False{} False{} True{} True{} True{}:
      {==}
    case False{} True{} False{} False{} False{}:
      {==}
    case False{} True{} False{} False{} True{}:
      {==}
    case False{} True{} False{} True{} False{}:
      {==}
    case False{} True{} False{} True{} True{}:
      {==}
    case False{} True{} True{} False{} False{}:
      {==}
    case False{} True{} True{} False{} True{}:
      {==}
    case False{} True{} True{} True{} False{}:
      {==}
    case False{} True{} True{} True{} True{}:
      {==}
    case True{} False{} False{} False{} False{}:
      {==}
    case True{} False{} False{} False{} True{}:
      {==}
    case True{} False{} False{} True{} False{}:
      {==}
    case True{} False{} False{} True{} True{}:
      {==}
    case True{} False{} True{} False{} False{}:
      {==}
    case True{} False{} True{} False{} True{}:
      {==}
    case True{} False{} True{} True{} False{}:
      {==}
    case True{} False{} True{} True{} True{}:
      {==}
    case True{} True{} False{} False{} False{}:
      {==}
    case True{} True{} False{} False{} True{}:
      {==}
    case True{} True{} False{} True{} False{}:
      {==}
    case True{} True{} False{} True{} True{}:
      {==}
    case True{} True{} True{} False{} False{}:
      {==}
    case True{} True{} True{} False{} True{}:
      {==}
    case True{} True{} True{} True{} False{}:
      {==}
    case True{} True{} True{} True{} True{}:
      {==}

law opContribs_kind:
  for +e: P.Env
  for +p: P.Policy
  for +o: P.Op
  {all_kind(P.op_contribs(e, p, o), P.PolicyCause{}) == True{} : Bool}
def opContribs_kind(e, p, o):
  match e p o:
    case P.Env{rs, inv, +table} P.Policy{m, rules, +um, +ui, +pu, +ps} P.Op{key, +w, +u, +i, +repo, +less}:
      op_kind_bits(i, u, Bool.or(List.is_empty(&2, String, ps), Bool.not(w)), Bool.and(P.repo_none(repo), Bool.not(less)), Bool.and(u, P.protected_any(ps, table, P.repo_value(repo))), ui, um, pu)

def kind_nc(+a: P.Cause, +k: P.Cause, he: {kind_eq(a, k) == True{} : Bool}, hk: {Bool.not(is_config(k)) == True{} : Bool}) -> {Bool.not(is_config(a)) == True{} : Bool}:
  match a k:
    case P.RuleCause{} P.RuleCause{}:
      {==}
    case P.RuleCause{} P.PolicyCause{}:
      {==}
    case P.RuleCause{} P.ConfigCause{}:
      {==}
    case P.PolicyCause{} P.RuleCause{}:
      {==}
    case P.PolicyCause{} P.PolicyCause{}:
      {==}
    case P.PolicyCause{} P.ConfigCause{}:
      {==}
    case P.ConfigCause{} P.RuleCause{}:
      P.absurd({Bool.not(is_config(P.ConfigCause{})) == True{} : Bool}, he)
    case P.ConfigCause{} P.PolicyCause{}:
      P.absurd({Bool.not(is_config(P.ConfigCause{})) == True{} : Bool}, he)
    case P.ConfigCause{} P.ConfigCause{}:
      P.absurd({Bool.not(is_config(P.ConfigCause{})) == True{} : Bool}, hk)

law all_kind_no_config:
  for +cs: +List<P.Contrib>
  for +k: P.Cause
  for +h: {all_kind(cs, k) == True{} : Bool}
  for +hk: {Bool.not(is_config(k)) == True{} : Bool}
  {no_config(cs) == True{} : Bool}
def all_kind_no_config(cs, k, h, hk):
  match cs:
    case Nil{}:
      {==}
    case Con{P.Contrib{d, +kk}, +t}:
      C.and_intro(Bool.not(is_config(kk)), no_config(t), kind_nc(kk, k, M.and_left(kind_eq(kk, k), all_kind(t, k), h), hk), all_kind_no_config(t, k, M.and_right(kind_eq(kk, k), all_kind(t, k), h), hk))

law rule_all_no_config:
  for +rs: +List<P.Rule>
  for +e: P.Env
  for +s: P.Subject
  {no_config(P.rule_all(rs, e, s)) == True{} : Bool}
def rule_all_no_config(rs, e, s):
  match rs:
    case Nil{}:
      {==}
    case Con{+r, +t}:
      Equal.trans(Bool, no_config(P.rule_all(r <> t, e, s)), Bool.and(no_config(P.rule_contribs(e, s, r)), no_config(P.rule_all(t, e, s))), True{}, no_config_cat(P.rule_contribs(e, s, r), P.rule_all(t, e, s)), C.and_intro(no_config(P.rule_contribs(e, s, r)), no_config(P.rule_all(t, e, s)), all_kind_no_config(P.rule_contribs(e, s, r), P.RuleCause{}, ruleContribs_kind(e, s, r), {==}), rule_all_no_config(t, e, s)))

law op_all_no_config:
  for +os: +List<P.Op>
  for +e: P.Env
  for +p: P.Policy
  {no_config(P.op_all(os, e, p)) == True{} : Bool}
def op_all_no_config(os, e, p):
  match os:
    case Nil{}:
      {==}
    case Con{+o, +t}:
      Equal.trans(Bool, no_config(P.op_all(o <> t, e, p)), Bool.and(no_config(P.op_contribs(e, p, o)), no_config(P.op_all(t, e, p))), True{}, no_config_cat(P.op_contribs(e, p, o), P.op_all(t, e, p)), C.and_intro(no_config(P.op_contribs(e, p, o)), no_config(P.op_all(t, e, p)), all_kind_no_config(P.op_contribs(e, p, o), P.PolicyCause{}, opContribs_kind(e, p, o), {==}), op_all_no_config(t, e, p)))

law contribs_kind:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  {no_config(P.contribs(e, p, s)) == True{} : Bool}
def contribs_kind(e, p, s):
  match p s:
    case P.Policy{+m, +rs, +um, +ui, +pu, +ps} P.Subject{+os, +no}:
      Equal.trans(Bool, no_config(P.contribs(e, P.Policy{m, rs, um, ui, pu, ps}, P.Subject{os, no})), Bool.and(no_config(P.rule_all(rs, e, P.Subject{os, no})), no_config(P.op_all(os, e, P.Policy{m, rs, um, ui, pu, ps}))), True{}, no_config_cat(P.rule_all(rs, e, P.Subject{os, no}), P.op_all(os, e, P.Policy{m, rs, um, ui, pu, ps})), C.and_intro(no_config(P.rule_all(rs, e, P.Subject{os, no})), no_config(P.op_all(os, e, P.Policy{m, rs, um, ui, pu, ps})), rule_all_no_config(rs, e, P.Subject{os, no}), op_all_no_config(os, e, P.Policy{m, rs, um, ui, pu, ps})))

law denials_content_cat:
  for +xs: +List<P.Cause>
  for +ys: +List<P.Cause>
  {denials_content(P.kcat(xs, ys)) == Bool.or(denials_content(xs), denials_content(ys)) : Bool}
def denials_content_cat(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{+k, +t}:
      %Equal.sym(Bool, denials_content(P.kcat(t, ys)), Bool.or(denials_content(t), denials_content(ys)), denials_content_cat(t, ys)) : {Bool.or(Bool.not(is_config(k)), _) == Bool.or(denials_content(k <> t), denials_content(ys)) : Bool}
      C.or_assoc(Bool.not(is_config(k)), denials_content(t), denials_content(ys))

law gate_content:
  for +m: P.Mode
  for +ev: P.Evaluation
  for +g: Bool
  {denials_content(P.ev_denials(gate_evaluation(m, ev, g))) == denials_content(P.ev_denials(ev)) : Bool}
def gate_content(m, ev, g):
  match g:
    case False{}:
      {==}
    case True{}:
      Equal.trans(Bool, denials_content(P.kcat(P.ev_denials(ev), [P.ConfigCause{}])), Bool.or(denials_content(P.ev_denials(ev)), False{}), denials_content(P.ev_denials(ev)), denials_content_cat(P.ev_denials(ev), [P.ConfigCause{}]), or_false_r(denials_content(P.ev_denials(ev))))

def any_content(cs: +List<P.Contrib>) -> Bool:
  match cs:
    case Nil{}:
      False{}
    case Con{P.Contrib{d, k}, t}:
      Bool.or(Bool.and(P.is_deny(d), Bool.not(is_config(k))), any_content(t))

law content_denials:
  for +cs: +List<P.Contrib>
  {denials_content(P.denials(cs)) == any_content(cs) : Bool}
def content_denials(cs):
  match cs:
    case Nil{}:
      {==}
    case Con{P.Contrib{P.Allow{}, k}, +t}:
      content_denials(t)
    case Con{P.Contrib{P.Audit{}, k}, +t}:
      content_denials(t)
    case Con{P.Contrib{P.Deny{}, +k}, +t}:
      %content_denials(t) : {Bool.or(Bool.not(is_config(k)), denials_content(P.denials(t))) == Bool.or(Bool.not(is_config(k)), _) : Bool}
      {==}

law content_from_no_config:
  for +cs: +List<P.Contrib>
  for h: {no_config(cs) == True{} : Bool}
  {any_content(cs) == P.any_deny(cs) : Bool}
def content_from_no_config(cs, h):
  match cs:
    case Nil{}:
      {==}
    case Con{P.Contrib{P.Allow{}, +k}, +t}:
      content_from_no_config(t, M.and_right(Bool.not(is_config(k)), no_config(t), h))
    case Con{P.Contrib{P.Audit{}, +k}, +t}:
      content_from_no_config(t, M.and_right(Bool.not(is_config(k)), no_config(t), h))
    case Con{P.Contrib{P.Deny{}, +k}, +t}:
      %Equal.sym(Bool, Bool.not(is_config(k)), True{}, M.and_left(Bool.not(is_config(k)), no_config(t), h)) : {Bool.or(_, any_content(t)) == True{} : Bool}
      {==}

law policy_content:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  {denials_content(P.ev_denials(evaluate_config(e, p, s, g))) == P.any_deny(P.contribs(e, p, s)) : Bool}
def policy_content(e, p, s, g):
  Equal.trans(Bool, denials_content(P.ev_denials(evaluate_config(e, p, s, g))), denials_content(P.denials(P.contribs(e, p, s))), P.any_deny(P.contribs(e, p, s)), gate_content(P.policy_mode(p), P.evaluate(e, p, s), g), Equal.trans(Bool, denials_content(P.denials(P.contribs(e, p, s))), any_content(P.contribs(e, p, s)), P.any_deny(P.contribs(e, p, s)), content_denials(P.contribs(e, p, s)), content_from_no_config(P.contribs(e, p, s), contribs_kind(e, p, s))))

law outs_content_eq:
  for +hs: +List<HookRun>
  {outs_content(outs(hs)) == hook_content(hs) : Bool}
def outs_content_eq(hs):
  match hs:
    case Nil{}:
      {==}
    case Con{HookRun{+h, +r}, +t}:
      %outs_content_eq(t) : {Bool.or(Bool.and(H.is_deny(H.eff(H.classify(H.Pre{}, h, r))), H.is_ok(r)), outs_content(outs(t))) == Bool.or(Bool.and(H.is_deny(H.eff(H.classify(H.Pre{}, h, r))), H.is_ok(r)), _) : Bool}
      {==}

law run_content:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  {content(evaluate_config(e, p, s, g), outs(hs)) == Bool.or(P.any_deny(P.contribs(e, p, s)), hook_content(hs)) : Bool}
def run_content(e, p, s, g, hs):
  %policy_content(e, p, s, g) : {Bool.or(denials_content(P.ev_denials(evaluate_config(e, p, s, g))), outs_content(outs(hs))) == Bool.or(_, hook_content(hs)) : Bool}
  %outs_content_eq(hs) : {Bool.or(denials_content(P.ev_denials(evaluate_config(e, p, s, g))), outs_content(outs(hs))) == Bool.or(denials_content(P.ev_denials(evaluate_config(e, p, s, g))), _) : Bool}
  {==}

# docs/hook-protocol.md:364: block -> content=77, errors/gates only=78;
# observe and non-blocking outcomes exit 0.
law run_exit:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +hs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  {exit_code(run(e, p, s, g, hs, ss, re)) == Bool.pick(Nat, is_deny(verdict(run(e, p, s, g, hs, ss, re))), Bool.pick(Nat, Bool.or(P.any_deny(P.contribs(e, p, s)), hook_content(hs)), 77n, 78n), 0n) : Nat}
def run_exit(e, p, s, g, hs, ss, re):
  %run_content(e, p, s, g, hs) : {exit_code(run(e, p, s, g, hs, ss, re)) == Bool.pick(Nat, is_deny(verdict(run(e, p, s, g, hs, ss, re))), Bool.pick(Nat, _, 77n, 78n), 0n) : Nat}
  decide_exit_bits(P.policy_mode(p), wd(evaluate_config(e, p, s, g), pre(hs)), content(evaluate_config(e, p, s, g), outs(hs)), ss, re, is_audit(P.ev_decision(evaluate_config(e, p, s, g))))

law ccat_nil:
  for +cs: +List<P.Contrib>
  {P.ccat(cs, []) == cs : +List<P.Contrib>}
def ccat_nil(cs):
  match cs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %ccat_nil(t) : {h <> P.ccat(t, []) == h <> _ : +List<P.Contrib>}
      {==}

law denials_cat:
  for +cs: +List<P.Contrib>
  for +ds: +List<P.Contrib>
  {P.denials(P.ccat(cs, ds)) == P.kcat(P.denials(cs), P.denials(ds)) : +List<P.Cause>}
def denials_cat(cs, ds):
  match cs:
    case Nil{}:
      {==}
    case Con{P.Contrib{P.Allow{}, k}, +t}:
      denials_cat(t, ds)
    case Con{P.Contrib{P.Audit{}, k}, +t}:
      denials_cat(t, ds)
    case Con{P.Contrib{P.Deny{}, +k}, +t}:
      %denials_cat(t, ds) : {k <> P.denials(P.ccat(t, ds)) == k <> _ : +List<P.Cause>}
      {==}

law eval_append_eq:
  for +m: P.Mode
  for +cs: +List<P.Contrib>
  for +ds: +List<P.Contrib>
  {P.eval_of(m, P.ccat(cs, ds)) == P.Evaluation{P.finalize(m, P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.is_deny(P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.kcat(P.denials(cs), P.denials(ds))} : P.Evaluation}
def eval_append_eq(m, cs, ds):
  %Equal.sym(+List<P.Decision>, P.decisions(P.ccat(cs, ds)), P.cat(P.decisions(cs), P.decisions(ds)), C.decisions_cat(cs, ds)) : {P.Evaluation{P.finalize(m, P.fold(_)), P.is_deny(P.fold(_)), P.denials(P.ccat(cs, ds))} == P.Evaluation{P.finalize(m, P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.is_deny(P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.kcat(P.denials(cs), P.denials(ds))} : P.Evaluation}
  %Equal.sym(P.Decision, P.fold(P.cat(P.decisions(cs), P.decisions(ds))), P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds))), P.fold_append(P.decisions(cs), P.decisions(ds))) : {P.Evaluation{P.finalize(m, _), P.is_deny(_), P.denials(P.ccat(cs, ds))} == P.Evaluation{P.finalize(m, P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.is_deny(P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.kcat(P.denials(cs), P.denials(ds))} : P.Evaluation}
  %denials_cat(cs, ds) : {P.Evaluation{P.finalize(m, P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.is_deny(P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.denials(P.ccat(cs, ds))} == P.Evaluation{P.finalize(m, P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), P.is_deny(P.merge(P.fold(P.decisions(cs)), P.fold(P.decisions(ds)))), _} : P.Evaluation}
  {==}

# A blocked gate is exactly one more deny/config contribution.
law evaluateConfig_eq:
  for +e: P.Env
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  {evaluate_config(e, p, s, g) == P.eval_of(P.policy_mode(p), P.ccat(P.contribs(e, p, s), P.one_if(g, P.Deny{}, P.ConfigCause{}))) : P.Evaluation}
def evaluateConfig_eq(e, p, s, g):
  match g:
    case False{}:
      %Equal.sym(+List<P.Contrib>, P.ccat(P.contribs(e, p, s), []), P.contribs(e, p, s), ccat_nil(P.contribs(e, p, s))) : {evaluate_config(e, p, s, False{}) == P.eval_of(P.policy_mode(p), _) : P.Evaluation}
      {==}
    case True{}:
      %Equal.sym(P.Evaluation, P.eval_of(P.policy_mode(p), P.ccat(P.contribs(e, p, s), [P.Contrib{P.Deny{}, P.ConfigCause{}}])), P.Evaluation{P.finalize(P.policy_mode(p), P.merge(P.fold(P.decisions(P.contribs(e, p, s))), P.Deny{})), P.is_deny(P.merge(P.fold(P.decisions(P.contribs(e, p, s))), P.Deny{})), P.kcat(P.denials(P.contribs(e, p, s)), [P.ConfigCause{}])}, eval_append_eq(P.policy_mode(p), P.contribs(e, p, s), [P.Contrib{P.Deny{}, P.ConfigCause{}}])) : {evaluate_config(e, p, s, True{}) == _ : P.Evaluation}
      %Equal.sym(P.Decision, P.merge(P.fold(P.decisions(P.contribs(e, p, s))), P.Deny{}), P.Deny{}, P.merge_deny_r(P.fold(P.decisions(P.contribs(e, p, s))))) : {evaluate_config(e, p, s, True{}) == P.Evaluation{P.finalize(P.policy_mode(p), _), P.is_deny(_), P.kcat(P.denials(P.contribs(e, p, s)), [P.ConfigCause{}])} : P.Evaluation}
      {==}

# BEGIN RUN OVERLAY PROOFS: no changes to the model or preceding laws.
# Exact propositional list membership: head equality or tail membership.
# Unlike comparing only effective decisions, this distinguishes the full HookRun.
def HookRunMember(-x: HookRun, xs: +List<HookRun>) -> Data:
  match xs:
    case Nil{}:
      Empty
    case Con{h, t}:
      Either<&2, &2, {x == h : HookRun}, HookRunMember(x, t)>

type HookDenyWitness<-xs: +List<HookRun>> is Data:
  HookWitness{hook: HookRun, member: HookRunMember(hook, xs), denies: {hr_denies(hook) == True{} : Bool}}

def lift_hook_witness(+head: HookRun, +tail: +List<HookRun>, witness: HookDenyWitness<tail>) -> HookDenyWitness<head <> tail>:
  match witness:
    case HookWitness{+x, mem, hd}:
      HookWitness{x, Inr{mem}, hd}

# The recursive continuation is affine and called once, only in the false case.
def choose_hook_witness(+head: HookRun, +tail: +List<HookRun>, +b: Bool, hb: {hr_denies(head) == b : Bool}, hd: {Bool.or(b, HookDenies(tail)) == True{} : Bool}, rest: {HookDenies(tail) == True{} : Bool} -> HookDenyWitness<tail>) -> HookDenyWitness<head <> tail>:
  match b:
    case False{}:
      lift_hook_witness(head, tail, rest(hd))
    case True{}:
      HookWitness{head, Inl{{==}}, hb}

def hook_deny_witness(+xs: +List<HookRun>, hd: {HookDenies(xs) == True{} : Bool}) -> HookDenyWitness<xs>:
  match xs:
    case Nil{}:
      P.absurd(HookDenyWitness<[]>, hd)
    case Con{HookRun{+h, +r}, +t}:
      choose_hook_witness(HookRun{h, r}, t, hr_denies(HookRun{h, r}), {==}, hd, ht => hook_deny_witness(t, ht))

def hook_member_denies(+xs: +List<HookRun>, +x: HookRun, mem: HookRunMember(x, xs), hd: {hr_denies(x) == True{} : Bool}) -> {HookDenies(xs) == True{} : Bool}:
  match xs:
    case Nil{}:
      match mem:
    case Con{HookRun{+h, +r}, +t}:
      match mem:
        case Inl{eq}:
          C.or_left(hr_denies(HookRun{h, r}), HookDenies(t), Equal.trans(Bool, hr_denies(HookRun{h, r}), hr_denies(x), True{}, Equal.sym(Bool, hr_denies(x), hr_denies(HookRun{h, r}), Equal.cong(HookRun, Bool, hr => hr_denies(hr), x, HookRun{h, r}, eq)), hd))
        case Inr{tail_mem}:
          C.or_right(hr_denies(HookRun{h, r}), HookDenies(t), hook_member_denies(t, x, tail_mem, hd))

def hook_witness_included(+xs: +List<HookRun>, +ys: +List<HookRun>, witness: HookDenyWitness<xs>, inc: @x: HookRun -> HookRunMember(x, xs) -> HookRunMember(x, ys)) -> {HookDenies(ys) == True{} : Bool}:
  match witness:
    case HookWitness{+x, mem, hd}:
      hook_member_denies(ys, x, inc(x, mem), hd)

def implies_intro(+a: Bool, +b: Bool, f: {a == True{} : Bool} -> {b == True{} : Bool}) -> {P.implies(a, b) == True{} : Bool}:
  match a:
    case False{}:
      {==}
    case True{}:
      f({==})

# Full list inclusion implies the weaker hook-denial-preservation hypothesis.
# inc is used only once on a witness, so no reusable closure is required.
law hook_denies_of_inclusion:
  for +xs: +List<HookRun>
  for +ys: +List<HookRun>
  for inc: @x: HookRun -> HookRunMember(x, xs) -> HookRunMember(x, ys)
  {P.implies(HookDenies(xs), HookDenies(ys)) == True{} : Bool}
def hook_denies_of_inclusion(xs, ys, inc):
  implies_intro(HookDenies(xs), HookDenies(ys), hd => hook_witness_included(xs, ys, hook_deny_witness(xs, hd), inc))

def deny_mono(+a: P.Decision, +b: P.Decision, hab: {P.le(a, b) == True{} : Bool}) -> {P.implies(P.is_deny(a), P.is_deny(b)) == True{} : Bool}:
  match a:
    case P.Allow{}:
      {==}
    case P.Audit{}:
      {==}
    case P.Deny{}:
      C.deny_up(P.Deny{}, b, hab, {==})

law policy_any_deny_overlay:
  for +e: P.Env
  for +pr: C.Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  {P.implies(P.any_deny(P.contribs(e, c, s)), P.any_deny(P.contribs(e, C.overlay(pr, c, p), s))) == True{} : Bool}
def policy_any_deny_overlay(e, pr, c, p, s):
  %P.evalOf_wouldDeny(P.policy_mode(c), P.contribs(e, c, s)) : {P.implies(_, P.any_deny(P.contribs(e, C.overlay(pr, c, p), s))) == True{} : Bool}
  %P.evalOf_wouldDeny(P.policy_mode(C.overlay(pr, c, p)), P.contribs(e, C.overlay(pr, c, p), s)) : {P.implies(P.is_deny(P.fold(P.decisions(P.contribs(e, c, s)))), _) == True{} : Bool}
  deny_mono(P.fold(P.decisions(P.contribs(e, c, s))), P.fold(P.decisions(P.contribs(e, C.overlay(pr, c, p), s))), C.overlay_fold_ge(e, pr, c, p, s))

law policy_denies_overlay:
  for +e: P.Env
  for +pr: C.Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +gg: Bool
  for hg: {P.implies(g, gg) == True{} : Bool}
  {P.implies(PolicyDenies(e, c, s, g), PolicyDenies(e, C.overlay(pr, c, p), s, gg)) == True{} : Bool}
def policy_denies_overlay(e, pr, c, p, s, g, gg, hg):
  M.or_mono(P.any_deny(P.contribs(e, c, s)), P.any_deny(P.contribs(e, C.overlay(pr, c, p), s)), g, gg, policy_any_deny_overlay(e, pr, c, p, s), hg)

def enforce_mono(+m: P.Mode, +n: P.Mode, hmn: {Nat.is_le(P.mode_rank(m), P.mode_rank(n)) == True{} : Bool}) -> {P.implies(P.is_enforce(m), P.is_enforce(n)) == True{} : Bool}:
  match m n:
    case P.Observe{} _:
      {==}
    case P.Enforce{} P.Observe{}:
      P.absurd({False{} == True{} : Bool}, hmn)
    case P.Enforce{} P.Enforce{}:
      {==}

# Stronger form of Lean's theorem: only preservation of hook-denial existence
# is needed, not full HookRun-list inclusion. A full-inclusion bridge follows.
# stdinSkipped/rulesEmpty may change independently, as in Lean.
law run_overlay_keeps_deny:
  for +e: P.Env
  for +pr: C.Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +gg: Bool
  for +hs: +List<HookRun>
  for +hhs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  for +ss2: Bool
  for +re2: Bool
  for hg: {P.implies(g, gg) == True{} : Bool}
  for hh: {P.implies(HookDenies(hs), HookDenies(hhs)) == True{} : Bool}
  for hd: {is_deny(verdict(run(e, c, s, g, hs, ss, re))) == True{} : Bool}
  {is_deny(verdict(run(e, C.overlay(pr, c, p), s, gg, hhs, ss2, re2))) == True{} : Bool}
def run_overlay_keeps_deny(e, pr, c, p, s, g, gg, hs, hhs, ss, re, ss2, re2, hg, hh, hd):
  %Equal.sym(Bool, is_deny(verdict(run(e, C.overlay(pr, c, p), s, gg, hhs, ss2, re2))), Bool.and(P.is_enforce(P.policy_mode(C.overlay(pr, c, p))), Bool.or(PolicyDenies(e, C.overlay(pr, c, p), s, gg), HookDenies(hhs))), run_deny_iff(e, C.overlay(pr, c, p), s, gg, hhs, ss2, re2)) : {_ == True{} : Bool}
  +cm = P.policy_mode(c)
  +nm = P.policy_mode(C.overlay(pr, c, p))
  +sources = Bool.or(PolicyDenies(e, c, s, g), HookDenies(hs))
  +sources2 = Bool.or(PolicyDenies(e, C.overlay(pr, c, p), s, gg), HookDenies(hhs))
  +parts = Equal.trans(Bool, Bool.and(P.is_enforce(cm), sources), is_deny(verdict(run(e, c, s, g, hs, ss, re))), True{}, Equal.sym(Bool, is_deny(verdict(run(e, c, s, g, hs, ss, re))), Bool.and(P.is_enforce(cm), sources), run_deny_iff(e, c, s, g, hs, ss, re)), hd)
  C.and_intro(P.is_enforce(nm), sources2, P.mp(P.is_enforce(cm), P.is_enforce(nm), enforce_mono(cm, nm, C.overlay_policy_mode_ge(pr, c, p)), M.and_left(P.is_enforce(cm), sources, parts)), P.mp(sources, sources2, M.or_mono(PolicyDenies(e, c, s, g), PolicyDenies(e, C.overlay(pr, c, p), s, gg), HookDenies(hs), HookDenies(hhs), policy_denies_overlay(e, pr, c, p, s, g, gg, hg), hh), M.and_right(P.is_enforce(cm), sources, parts)))

# Literal Lean hypotheses: g -> gg and every full user HookRun occurs in hhs.
law run_overlay_keeps_deny_from_inclusion:
  for +e: P.Env
  for +pr: C.Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  for +g: Bool
  for +gg: Bool
  for +hs: +List<HookRun>
  for +hhs: +List<HookRun>
  for +ss: Bool
  for +re: Bool
  for +ss2: Bool
  for +re2: Bool
  for hg: {P.implies(g, gg) == True{} : Bool}
  for inc: @x: HookRun -> HookRunMember(x, hs) -> HookRunMember(x, hhs)
  for hd: {is_deny(verdict(run(e, c, s, g, hs, ss, re))) == True{} : Bool}
  {is_deny(verdict(run(e, C.overlay(pr, c, p), s, gg, hhs, ss2, re2))) == True{} : Bool}
def run_overlay_keeps_deny_from_inclusion(e, pr, c, p, s, g, gg, hs, hhs, ss, re, ss2, re2, hg, inc, hd):
  run_overlay_keeps_deny(e, pr, c, p, s, g, gg, hs, hhs, ss, re, ss2, re2, hg, hook_denies_of_inclusion(hs, hhs, inc), hd)
# END RUN OVERLAY PROOFS.
