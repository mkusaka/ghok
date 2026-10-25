# Port of Ghok/Policy.lean; Go line references retained below.
# Prop membership/existentials/iff use Bool functions and equalities; <= uses
# the Bool order le, extensionally rank order. Base has Empty, Either, Sigma,
# Maybe (no Or or Permutation); Empty is used for contradictory hypotheses.
# Env is a finite, precomputed lookup table, not affine closures. For the finite
# subject/rule/protected-repository inputs, cache all queried matcher results;
# unknown keys default to false. Tables key rules by (id, decision); Op.key
# supplies stable operation identity, assigned consistently to equal input ops.
# Policy matcher calls include opMatch (match.go:211), onUnresolved:ignore and
# unless. Only considered operations are present. warn maps to Allow through
# handling's fallback (evaluate.go:64); abstain never enters the policy fold.
import Base

type Decision is Data:
  Allow{}
  Audit{}
  Deny{}

type Mode is Data:
  Observe{}
  Enforce{}

# evaluate.go:55
def merge(a: Decision, b: Decision) -> Decision:
  match a b:
    case Deny{} _:
      Deny{}
    case Audit{} Deny{}:
      Deny{}
    case Audit{} _:
      Audit{}
    case Allow{} b:
      b

# e.Decision starts at Allow and merges every contribution
def fold(xs: +List<Decision>) -> Decision:
  match xs:
    case Nil{}:
      Allow{}
    case Con{h, t}:
      merge(h, fold(t))

def cat(xs: +List<Decision>, ys: +List<Decision>) -> +List<Decision>:
  match xs:
    case Nil{}:
      ys
    case Con{h, t}:
      h <> cat(t, ys)

# observe mode turns a would-be deny into audit
def finalize(m: Mode, d: Decision) -> Decision:
  match m d:
    case Observe{} Deny{}:
      Audit{}
    case _ d:
      d

def is_deny(d: Decision) -> Bool:
  match d:
    case Deny{}:
      True{}
    case _:
      False{}

def has_deny(xs: +List<Decision>) -> Bool:
  match xs:
    case Nil{}:
      False{}
    case Con{h, t}:
      Bool.or(is_deny(h), has_deny(t))

# Allow < Audit < Deny
def le(a: Decision, b: Decision) -> Bool:
  match a b:
    case Allow{} _:
      True{}
    case Audit{} Allow{}:
      False{}
    case Audit{} _:
      True{}
    case Deny{} Deny{}:
      True{}
    case Deny{} _:
      False{}

def implies(a: Bool, b: Bool) -> Bool:
  Bool.or(Bool.not(a), b)

def false_motive(b: Bool) -> Type:
  match b:
    case False{}:
      Unit
    case True{}:
      Empty

def false_true(e: {False{} == True{} : Bool}) -> Empty:
  %e : false_motive(_)
  Unit{}

def empty_elim(-A: Type, v: Empty) -> A:
  match v:

def absurd(-A: Type, e: {False{} == True{} : Bool}) -> A:
  empty_elim(A, false_true(e))

def mp(+a: Bool, +b: Bool, h: {implies(a, b) == True{} : Bool}, ha: {a == True{} : Bool}) -> {b == True{} : Bool}:
  match a:
    case False{}:
      absurd({b == True{} : Bool}, ha)
    case True{}:
      h

def rank(d: Decision) -> Nat:
  match d:
    case Allow{}:
      0n
    case Audit{}:
      1n
    case Deny{}:
      2n

def mode_rank(m: Mode) -> Nat:
  match m:
    case Observe{}:
      0n
    case Enforce{}:
      1n

def is_enforce(m: Mode) -> Bool:
  match m:
    case Observe{}:
      False{}
    case Enforce{}:
      True{}

def eq(+a: Decision, +b: Decision) -> Bool:
  Bool.and(le(a, b), le(b, a))

def mem(+x: Decision, xs: +List<Decision>) -> Bool:
  match xs:
    case Nil{}:
      False{}
    case Con{h, t}:
      Bool.or(eq(x, h), mem(x, t))

def dominated_one(+x: Decision, ys: +List<Decision>) -> Bool:
  match ys:
    case Nil{}:
      False{}
    case Con{y, t}:
      Bool.or(le(x, y), dominated_one(x, t))

def dominated(xs: +List<Decision>, +ys: +List<Decision>) -> Bool:
  match xs:
    case Nil{}:
      True{}
    case Con{h, t}:
      Bool.and(dominated_one(h, ys), dominated(t, ys))

law merge_comm:
  for +a: Decision
  for +b: Decision
  {merge(a, b) == merge(b, a) : Decision}

def merge_comm(a, b):
  match a b:
    case Allow{} Allow{}:
      {==}
    case Allow{} Audit{}:
      {==}
    case Allow{} Deny{}:
      {==}
    case Audit{} Allow{}:
      {==}
    case Audit{} Audit{}:
      {==}
    case Audit{} Deny{}:
      {==}
    case Deny{} Allow{}:
      {==}
    case Deny{} Audit{}:
      {==}
    case Deny{} Deny{}:
      {==}

law merge_assoc:
  for +a: Decision
  for +b: Decision
  for +c: Decision
  {merge(merge(a, b), c) == merge(a, merge(b, c)) : Decision}

def merge_assoc(a, b, c):
  match a b c:
    case Allow{} Allow{} Allow{}:
      {==}
    case Allow{} Allow{} Audit{}:
      {==}
    case Allow{} Allow{} Deny{}:
      {==}
    case Allow{} Audit{} Allow{}:
      {==}
    case Allow{} Audit{} Audit{}:
      {==}
    case Allow{} Audit{} Deny{}:
      {==}
    case Allow{} Deny{} Allow{}:
      {==}
    case Allow{} Deny{} Audit{}:
      {==}
    case Allow{} Deny{} Deny{}:
      {==}
    case Audit{} Allow{} Allow{}:
      {==}
    case Audit{} Allow{} Audit{}:
      {==}
    case Audit{} Allow{} Deny{}:
      {==}
    case Audit{} Audit{} Allow{}:
      {==}
    case Audit{} Audit{} Audit{}:
      {==}
    case Audit{} Audit{} Deny{}:
      {==}
    case Audit{} Deny{} Allow{}:
      {==}
    case Audit{} Deny{} Audit{}:
      {==}
    case Audit{} Deny{} Deny{}:
      {==}
    case Deny{} Allow{} Allow{}:
      {==}
    case Deny{} Allow{} Audit{}:
      {==}
    case Deny{} Allow{} Deny{}:
      {==}
    case Deny{} Audit{} Allow{}:
      {==}
    case Deny{} Audit{} Audit{}:
      {==}
    case Deny{} Audit{} Deny{}:
      {==}
    case Deny{} Deny{} Allow{}:
      {==}
    case Deny{} Deny{} Audit{}:
      {==}
    case Deny{} Deny{} Deny{}:
      {==}

law merge_idem:
  for +a: Decision
  {merge(a, a) == a : Decision}

def merge_idem(a):
  match a:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law merge_allow_l:
  for +a: Decision
  {merge(Allow{}, a) == a : Decision}

def merge_allow_l(a):
  match a:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law merge_allow_r:
  for +a: Decision
  {merge(a, Allow{}) == a : Decision}

def merge_allow_r(a):
  match a:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law merge_deny_r:
  for +a: Decision
  {merge(a, Deny{}) == Deny{} : Decision}

def merge_deny_r(a):
  match a:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law merge_max:
  for +a: Decision
  for +b: Decision
  {rank(merge(a, b)) == Nat.max(rank(a), rank(b)) : Nat}

def merge_max(a, b):
  match a b:
    case Allow{} Allow{}:
      {==}
    case Allow{} Audit{}:
      {==}
    case Allow{} Deny{}:
      {==}
    case Audit{} Allow{}:
      {==}
    case Audit{} Audit{}:
      {==}
    case Audit{} Deny{}:
      {==}
    case Deny{} Allow{}:
      {==}
    case Deny{} Audit{}:
      {==}
    case Deny{} Deny{}:
      {==}

law rank_inj:
  for +a: Decision
  for +b: Decision
  {implies(Nat.is_eq(rank(a), rank(b)), eq(a, b)) == True{} : Bool}

def rank_inj(a, b):
  match a b:
    case Allow{} Allow{}:
      {==}
    case Allow{} Audit{}:
      {==}
    case Allow{} Deny{}:
      {==}
    case Audit{} Allow{}:
      {==}
    case Audit{} Audit{}:
      {==}
    case Audit{} Deny{}:
      {==}
    case Deny{} Allow{}:
      {==}
    case Deny{} Audit{}:
      {==}
    case Deny{} Deny{}:
      {==}

law rank_le_two:
  for +a: Decision
  {Nat.is_le(rank(a), 2n) == True{} : Bool}

def rank_le_two(a):
  match a:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law rank_eq_two:
  for +a: Decision
  {Nat.is_eq(rank(a), 2n) == is_deny(a) : Bool}

def rank_eq_two(a):
  match a:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law le_rank:
  for +a: Decision
  for +b: Decision
  {le(a, b) == Nat.is_le(rank(a), rank(b)) : Bool}

def le_rank(a, b):
  match a b:
    case Allow{} Allow{}:
      {==}
    case Allow{} Audit{}:
      {==}
    case Allow{} Deny{}:
      {==}
    case Audit{} Allow{}:
      {==}
    case Audit{} Audit{}:
      {==}
    case Audit{} Deny{}:
      {==}
    case Deny{} Allow{}:
      {==}
    case Deny{} Audit{}:
      {==}
    case Deny{} Deny{}:
      {==}

law merge_up:
  for +a: Decision
  for +b: Decision
  {le(a, merge(a, b)) == True{} : Bool}

def merge_up(a, b):
  match a b:
    case Allow{} Allow{}:
      {==}
    case Allow{} Audit{}:
      {==}
    case Allow{} Deny{}:
      {==}
    case Audit{} Allow{}:
      {==}
    case Audit{} Audit{}:
      {==}
    case Audit{} Deny{}:
      {==}
    case Deny{} Allow{}:
      {==}
    case Deny{} Audit{}:
      {==}
    case Deny{} Deny{}:
      {==}

law is_deny_merge:
  for +a: Decision
  for +b: Decision
  {is_deny(merge(a, b)) == Bool.or(is_deny(a), is_deny(b)) : Bool}

def is_deny_merge(a, b):
  match a b:
    case Allow{} Allow{}:
      {==}
    case Allow{} Audit{}:
      {==}
    case Allow{} Deny{}:
      {==}
    case Audit{} Allow{}:
      {==}
    case Audit{} Audit{}:
      {==}
    case Audit{} Deny{}:
      {==}
    case Deny{} Allow{}:
      {==}
    case Deny{} Audit{}:
      {==}
    case Deny{} Deny{}:
      {==}

law fold_nil:
  {fold([]) == Allow{} : Decision}
def fold_nil():
  {==}

law fold_cons:
  for +h: Decision
  for +t: +List<Decision>
  {fold(h <> t) == merge(h, fold(t)) : Decision}
def fold_cons(h, t):
  {==}

law fold_append:
  for +xs: +List<Decision>
  for +ys: +List<Decision>
  {fold(cat(xs, ys)) == merge(fold(xs), fold(ys)) : Decision}

def fold_append(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %Equal.sym(Decision, fold(cat(t, ys)), merge(fold(t), fold(ys)), fold_append(t, ys)) : {merge(h, _) == merge(merge(h, fold(t)), fold(ys)) : Decision}
      Equal.sym(Decision, merge(merge(h, fold(t)), fold(ys)), merge(h, merge(fold(t), fold(ys))), merge_assoc(h, fold(t), fold(ys)))

# "No allow rule can override a deny or audit": appending Allow is a no-op
law allow_noop:
  for +xs: +List<Decision>
  {fold(cat(xs, [Allow{}])) == fold(xs) : Decision}

def allow_noop(xs):
  Equal.trans(Decision, fold(cat(xs, [Allow{}])), merge(fold(xs), Allow{}), fold(xs), fold_append(xs, [Allow{}]), merge_allow_r(fold(xs)))

# any contribution only raises the decision
law fold_monotone:
  for +xs: +List<Decision>
  for +d: Decision
  {le(fold(xs), fold(cat(xs, [d]))) == True{} : Bool}

def fold_monotone(xs, d):
  %Equal.sym(Decision, fold(cat(xs, [d])), merge(fold(xs), merge(d, Allow{})), fold_append(xs, [d])) : {le(fold(xs), _) == True{} : Bool}
  %Equal.sym(Decision, merge(d, Allow{}), d, merge_allow_r(d)) : {le(fold(xs), merge(fold(xs), _)) == True{} : Bool}
  merge_up(fold(xs), d)

# evaluate.go:80: no allow rule overrides deny or audit.
law allow_anywhere:
  for +xs: +List<Decision>
  for +ys: +List<Decision>
  {fold(cat(xs, Allow{} <> ys)) == fold(cat(xs, ys)) : Decision}
def allow_anywhere(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %allow_anywhere(t, ys) : {merge(h, fold(cat(t, Allow{} <> ys))) == merge(h, _) : Decision}
      {==}

law rank_fold_cons:
  for +h: Decision
  for +t: +List<Decision>
  {rank(fold(h <> t)) == Nat.max(rank(h), rank(fold(t))) : Nat}
def rank_fold_cons(h, t):
  merge_max(h, fold(t))

law would_deny_iff:
  for +xs: +List<Decision>
  {is_deny(fold(xs)) == has_deny(xs) : Bool}

def would_deny_iff(xs):
  match xs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %would_deny_iff(t) : {is_deny(merge(h, fold(t))) == Bool.or(is_deny(h), _) : Bool}
      is_deny_merge(h, fold(t))

# evaluate.go:206-216: observe downgrades deny to audit.

law observe_never_denies:
  for +d: Decision
  {is_deny(finalize(Observe{}, d)) == False{} : Bool}

def observe_never_denies(d):
  match d:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

# downgrading the total equals merging the downgraded parts, so the
# e.Decision and e.PerOp downgrades in Evaluate agree
law finalize_hom:
  for +m: Mode
  for +a: Decision
  for +b: Decision
  {finalize(m, merge(a, b)) == merge(finalize(m, a), finalize(m, b)) : Decision}

def finalize_hom(m, a, b):
  match m a b:
    case Enforce{} _ _:
      {==}
    case Observe{} Deny{} Allow{}:
      {==}
    case Observe{} Deny{} Audit{}:
      {==}
    case Observe{} Deny{} Deny{}:
      {==}
    case Observe{} Allow{} Allow{}:
      {==}
    case Observe{} Allow{} Audit{}:
      {==}
    case Observe{} Allow{} Deny{}:
      {==}
    case Observe{} Audit{} Allow{}:
      {==}
    case Observe{} Audit{} Audit{}:
      {==}
    case Observe{} Audit{} Deny{}:
      {==}
def map_finalize(+m: Mode, xs: +List<Decision>) -> +List<Decision>:
  match xs:
    case Nil{}:
      []
    case Con{h, t}:
      finalize(m, h) <> map_finalize(m, t)

law finalize_fold:
  for +m: Mode
  for +xs: +List<Decision>
  {finalize(m, fold(xs)) == fold(map_finalize(m, xs)) : Decision}
def finalize_fold(m, xs):
  match m xs:
    case Observe{} Nil{}:
      {==}
    case Enforce{} Nil{}:
      {==}
    case m Con{+h, +t}:
      %finalize_fold(m, t) : {finalize(m, merge(h, fold(t))) == merge(finalize(m, h), _) : Decision}
      finalize_hom(m, h, fold(t))

law finalize_deny_iff:
  for +m: Mode
  for +d: Decision
  {is_deny(finalize(m, d)) == Bool.and(is_enforce(m), is_deny(d)) : Bool}

def finalize_deny_iff(m, d):
  match m d:
    case Observe{} Allow{}:
      {==}
    case Observe{} Audit{}:
      {==}
    case Observe{} Deny{}:
      {==}
    case Enforce{} Allow{}:
      {==}
    case Enforce{} Audit{}:
      {==}
    case Enforce{} Deny{}:
      {==}

law finalize_mono:
  for +m: Mode
  for +n: Mode
  for +d: Decision
  for +e: Decision
  {implies(Bool.and(Nat.is_le(mode_rank(m), mode_rank(n)), le(d, e)), le(finalize(m, d), finalize(n, e))) == True{} : Bool}

def finalize_mono(m, n, d, e):
  match m n d e:
    case Observe{} Observe{} Allow{} Allow{}:
      {==}
    case Observe{} Observe{} Allow{} Audit{}:
      {==}
    case Observe{} Observe{} Allow{} Deny{}:
      {==}
    case Observe{} Observe{} Audit{} Allow{}:
      {==}
    case Observe{} Observe{} Audit{} Audit{}:
      {==}
    case Observe{} Observe{} Audit{} Deny{}:
      {==}
    case Observe{} Observe{} Deny{} Allow{}:
      {==}
    case Observe{} Observe{} Deny{} Audit{}:
      {==}
    case Observe{} Observe{} Deny{} Deny{}:
      {==}
    case Observe{} Enforce{} Allow{} Allow{}:
      {==}
    case Observe{} Enforce{} Allow{} Audit{}:
      {==}
    case Observe{} Enforce{} Allow{} Deny{}:
      {==}
    case Observe{} Enforce{} Audit{} Allow{}:
      {==}
    case Observe{} Enforce{} Audit{} Audit{}:
      {==}
    case Observe{} Enforce{} Audit{} Deny{}:
      {==}
    case Observe{} Enforce{} Deny{} Allow{}:
      {==}
    case Observe{} Enforce{} Deny{} Audit{}:
      {==}
    case Observe{} Enforce{} Deny{} Deny{}:
      {==}
    case Enforce{} Observe{} Allow{} Allow{}:
      {==}
    case Enforce{} Observe{} Allow{} Audit{}:
      {==}
    case Enforce{} Observe{} Allow{} Deny{}:
      {==}
    case Enforce{} Observe{} Audit{} Allow{}:
      {==}
    case Enforce{} Observe{} Audit{} Audit{}:
      {==}
    case Enforce{} Observe{} Audit{} Deny{}:
      {==}
    case Enforce{} Observe{} Deny{} Allow{}:
      {==}
    case Enforce{} Observe{} Deny{} Audit{}:
      {==}
    case Enforce{} Observe{} Deny{} Deny{}:
      {==}
    case Enforce{} Enforce{} Allow{} Allow{}:
      {==}
    case Enforce{} Enforce{} Allow{} Audit{}:
      {==}
    case Enforce{} Enforce{} Allow{} Deny{}:
      {==}
    case Enforce{} Enforce{} Audit{} Allow{}:
      {==}
    case Enforce{} Enforce{} Audit{} Audit{}:
      {==}
    case Enforce{} Enforce{} Audit{} Deny{}:
      {==}
    case Enforce{} Enforce{} Deny{} Allow{}:
      {==}
    case Enforce{} Enforce{} Deny{} Audit{}:
      {==}
    case Enforce{} Enforce{} Deny{} Deny{}:
      {==}

# Who caused the contribution / Go Denial.Kind (Cause avoids reserved Kind).
# ConfigCause is the trust-blocked gate (cli/decide_unix.go:80).
type Cause is Data:
  RuleCause{}
  PolicyCause{}
  ConfigCause{}

# write means op.Effect != read; unclassified is evaluate.go:76.
# input_unknown is UnknownInputUnavailable / UnknownInputTooLarge.
# repo None is targetValues' empty string; RepositoryLess is match.go:138.
type Op is Data:
  Op{key: Nat, write: Bool, unclassified: Bool, input_unknown: Bool, repo: +Maybe<String>, repository_less: Bool}
type Rule is Data:
  Rule{id: String, decision: Decision}
# Mode normalizes anything except enforce to Observe (evaluate.go:83).
type Policy is Data:
  Policy{mode: Mode, rules: +List<Rule>, unknown_mutation: Decision, unknown_input: Decision, protected_unknown: Decision, protected_repos: +List<String>}
type Subject is Data:
  Subject{ops: +List<Op>, no_ops: Bool}
type RuleHit is Data:
  RuleHit{rule: String, decision: Decision, op: Nat, hit: Bool}
type InvHit is Data:
  InvHit{rule: String, decision: Decision, hit: Bool}
type ProtHit is Data:
  ProtHit{pattern: String, value: String, hit: Bool}
type Env is Data:
  Env{rules: +List<RuleHit>, inv: +List<InvHit>, prot: +List<ProtHit>}
type Contrib is Data:
  Contrib{decision: Decision, kind: Cause}
type Evaluation is Data:
  Evaluation{decision: Decision, would_deny: Bool, denials: +List<Cause>}

def policy_mode(p: Policy) -> Mode:
  Policy{m, rs, um, ui, pu, ps} = p
  m

def lookup_rule(xs: +List<RuleHit>, +id: String, +d: Decision, +key: Nat) -> Bool:
  match xs:
    case Nil{}:
      False{}
    case Con{RuleHit{r, rd, o, b}, t}:
      Bool.pick(Bool, Bool.and(String.eq(id, r), Bool.and(eq(d, rd), Nat.is_eq(key, o))), b, lookup_rule(t, id, d, key))

def lookup_inv(xs: +List<InvHit>, +id: String, +d: Decision) -> Bool:
  match xs:
    case Nil{}:
      False{}
    case Con{InvHit{r, rd, b}, t}:
      Bool.pick(Bool, Bool.and(String.eq(id, r), eq(d, rd)), b, lookup_inv(t, id, d))

def lookup_prot(xs: +List<ProtHit>, +pat: String, +value: String) -> Bool:
  match xs:
    case Nil{}:
      False{}
    case Con{ProtHit{p, v, b}, t}:
      Bool.pick(Bool, Bool.and(String.eq(pat, p), String.eq(value, v)), b, lookup_prot(t, pat, value))

def fires(e: Env, r: Rule, o: Op) -> Bool:
  Env{rs, inv, ps} = e
  Rule{id, d} = r
  Op{key, w, u, i, repo, less} = o
  lookup_rule(rs, id, d, key)

def fires_inv(e: Env, r: Rule) -> Bool:
  Env{rs, inv, ps} = e
  Rule{id, d} = r
  lookup_inv(inv, id, d)

def repo_value(r: +Maybe<String>) -> String:
  match r:
    case None{}:
      ""
    case Some{v}:
      v

def repo_none(r: +Maybe<String>) -> Bool:
  match r:
    case None{}:
      True{}
    case Some{v}:
      False{}

def protected_any(ps: +List<String>, +table: +List<ProtHit>, +value: String) -> Bool:
  match ps:
    case Nil{}:
      False{}
    case Con{p, t}:
      Bool.or(lookup_prot(table, p, value), protected_any(t, table, value))

def ccat(xs: +List<Contrib>, ys: +List<Contrib>) -> +List<Contrib>:
  List.append(&2, Contrib, xs, ys)

def kcat(xs: +List<Cause>, ys: +List<Cause>) -> +List<Cause>:
  List.append(&2, Cause, xs, ys)

def one_if(b: Bool, d: Decision, k: Cause) -> +List<Contrib>:
  Bool.pick(+List<Contrib>, b, [Contrib{d, k}], [])

def rule_ops(os: +List<Op>, +e: Env, +r: Rule) -> +List<Contrib>:
  match os:
    case Nil{}:
      []
    case Con{+o, t}:
      Rule{id, +d} = r
      ccat(one_if(fires(e, Rule{id, d}, o), d, RuleCause{}), rule_ops(t, e, r))

# evaluate.go:131-161
def rule_contribs(+e: Env, s: Subject, +r: Rule) -> +List<Contrib>:
  Subject{os, no} = s
  Rule{id, d} = r
  ccat(one_if(Bool.and(no, fires_inv(e, r)), d, RuleCause{}), rule_ops(os, e, r))

# evaluate.go:162-182
def op_contribs(e: Env, p: Policy, o: Op) -> +List<Contrib>:
  Env{rs, inv, table} = e
  Policy{m, rules, um, ui, +pu, +ps} = p
  Op{key, +w, +u, i, +repo, less} = o
  first = Bool.pick(+List<Contrib>, i, [Contrib{ui, PolicyCause{}}], one_if(u, um, PolicyCause{}))
  blocked = Bool.or(List.is_empty(&2, String, ps), Bool.not(w))
  unresolved = Bool.and(repo_none(repo), Bool.not(less))
  protected = Bool.and(u, protected_any(ps, table, repo_value(repo)))
  second = Bool.pick(+List<Contrib>, blocked, [], Bool.pick(+List<Contrib>, unresolved, [Contrib{pu, PolicyCause{}}], one_if(protected, pu, PolicyCause{})))
  ccat(first, second)

def rule_all(rs: +List<Rule>, +e: Env, +s: Subject) -> +List<Contrib>:
  match rs:
    case Nil{}:
      []
    case Con{r, t}:
      ccat(rule_contribs(e, s, r), rule_all(t, e, s))

def op_all(os: +List<Op>, +e: Env, +p: Policy) -> +List<Contrib>:
  match os:
    case Nil{}:
      []
    case Con{o, t}:
      ccat(op_contribs(e, p, o), op_all(t, e, p))

def contribs(+e: Env, +p: Policy, +s: Subject) -> +List<Contrib>:
  Policy{m, rs, um, ui, pu, ps} = p
  Subject{os, no} = s
  ccat(rule_all(rs, e, s), op_all(os, e, p))

def decisions(cs: +List<Contrib>) -> +List<Decision>:
  match cs:
    case Nil{}:
      []
    case Con{Contrib{d, k}, t}:
      d <> decisions(t)

def denials(cs: +List<Contrib>) -> +List<Cause>:
  match cs:
    case Nil{}:
      []
    case Con{Contrib{d, k}, +t}:
      Bool.pick(+List<Cause>, is_deny(d), k <> denials(t), denials(t))

def any_deny(cs: +List<Contrib>) -> Bool:
  match cs:
    case Nil{}:
      False{}
    case Con{Contrib{d, k}, t}:
      Bool.or(is_deny(d), any_deny(t))

def eval_of(+m: Mode, +cs: +List<Contrib>) -> Evaluation:
  Evaluation{finalize(m, fold(decisions(cs))), is_deny(fold(decisions(cs))), denials(cs)}

def evaluate(+e: Env, +p: Policy, +s: Subject) -> Evaluation:
  eval_of(policy_mode(p), contribs(e, p, s))

def ev_decision(e: Evaluation) -> Decision:
  Evaluation{d, wd, ds} = e
  d

def ev_would_deny(e: Evaluation) -> Bool:
  Evaluation{d, wd, ds} = e
  wd

def ev_denials(e: Evaluation) -> +List<Cause>:
  Evaluation{d, wd, ds} = e
  ds

law has_deny_decisions:
  for +cs: +List<Contrib>
  {has_deny(decisions(cs)) == any_deny(cs) : Bool}
def has_deny_decisions(cs):
  match cs:
    case Nil{}:
      {==}
    case Con{Contrib{+d, k}, +t}:
      %has_deny_decisions(t) : {Bool.or(is_deny(d), has_deny(decisions(t))) == Bool.or(is_deny(d), _) : Bool}
      {==}

law evalOf_wouldDeny:
  for +m: Mode
  for +cs: +List<Contrib>
  {ev_would_deny(eval_of(m, cs)) == any_deny(cs) : Bool}
def evalOf_wouldDeny(m, cs):
  Equal.trans(Bool, is_deny(fold(decisions(cs))), has_deny(decisions(cs)), any_deny(cs), would_deny_iff(decisions(cs)), has_deny_decisions(cs))

law denials_nonempty:
  for +cs: +List<Contrib>
  {Bool.not(List.is_empty(&2, Cause, denials(cs))) == any_deny(cs) : Bool}
def denials_nonempty(cs):
  match cs:
    case Nil{}:
      {==}
    case Con{Contrib{Allow{}, k}, +t}:
      denials_nonempty(t)
    case Con{Contrib{Audit{}, k}, +t}:
      denials_nonempty(t)
    case Con{Contrib{Deny{}, k}, t}:
      {==}

law evalOf_denials:
  for +m: Mode
  for +cs: +List<Contrib>
  {Bool.not(List.is_empty(&2, Cause, ev_denials(eval_of(m, cs)))) == ev_would_deny(eval_of(m, cs)) : Bool}
def evalOf_denials(m, cs):
  Equal.trans(Bool, Bool.not(List.is_empty(&2, Cause, denials(cs))), any_deny(cs), is_deny(fold(decisions(cs))), denials_nonempty(cs), Equal.sym(Bool, is_deny(fold(decisions(cs))), any_deny(cs), evalOf_wouldDeny(m, cs)))

law evalOf_decision_deny:
  for +m: Mode
  for +cs: +List<Contrib>
  {is_deny(ev_decision(eval_of(m, cs))) == Bool.and(is_enforce(m), ev_would_deny(eval_of(m, cs))) : Bool}
def evalOf_decision_deny(m, cs):
  finalize_deny_iff(m, fold(decisions(cs)))

law evalOf_observe:
  for +cs: +List<Contrib>
  {is_deny(ev_decision(eval_of(Observe{}, cs))) == False{} : Bool}
def evalOf_observe(cs):
  observe_never_denies(fold(decisions(cs)))

law evaluate_enforce_deny:
  for +e: Env
  for +p: Policy
  for +s: Subject
  for hm: {policy_mode(p) == Enforce{} : Mode}
  for h: {any_deny(contribs(e, p, s)) == True{} : Bool}
  {is_deny(ev_decision(evaluate(e, p, s))) == True{} : Bool}
def evaluate_enforce_deny(e, p, s, hm, h):
  %Equal.sym(Mode, policy_mode(p), Enforce{}, hm) : {is_deny(finalize(_, fold(decisions(contribs(e, p, s))))) == True{} : Bool}
  Equal.trans(Bool, is_deny(fold(decisions(contribs(e, p, s)))), any_deny(contribs(e, p, s)), True{}, evalOf_wouldDeny(Enforce{}, contribs(e, p, s)), h)
def mem_step(+x: Decision, +h: Decision, +d: Decision, +m: Bool, ih: {implies(m, le(x, d)) == True{} : Bool}) -> {implies(Bool.or(eq(x, h), m), le(x, merge(h, d))) == True{} : Bool}:
  match x h d m:
    case Allow{} Allow{} Allow{} False{}:
      {==}
    case Allow{} Allow{} Allow{} True{}:
      {==}
    case Allow{} Allow{} Audit{} False{}:
      {==}
    case Allow{} Allow{} Audit{} True{}:
      {==}
    case Allow{} Allow{} Deny{} False{}:
      {==}
    case Allow{} Allow{} Deny{} True{}:
      {==}
    case Allow{} Audit{} Allow{} False{}:
      {==}
    case Allow{} Audit{} Allow{} True{}:
      {==}
    case Allow{} Audit{} Audit{} False{}:
      {==}
    case Allow{} Audit{} Audit{} True{}:
      {==}
    case Allow{} Audit{} Deny{} False{}:
      {==}
    case Allow{} Audit{} Deny{} True{}:
      {==}
    case Allow{} Deny{} Allow{} False{}:
      {==}
    case Allow{} Deny{} Allow{} True{}:
      {==}
    case Allow{} Deny{} Audit{} False{}:
      {==}
    case Allow{} Deny{} Audit{} True{}:
      {==}
    case Allow{} Deny{} Deny{} False{}:
      {==}
    case Allow{} Deny{} Deny{} True{}:
      {==}
    case Audit{} Allow{} Allow{} False{}:
      {==}
    case Audit{} Allow{} Allow{} True{}:
      absurd({implies(Bool.or(eq(Audit{}, Allow{}), True{}), le(Audit{}, merge(Allow{}, Allow{}))) == True{} : Bool}, ih)
    case Audit{} Allow{} Audit{} False{}:
      {==}
    case Audit{} Allow{} Audit{} True{}:
      {==}
    case Audit{} Allow{} Deny{} False{}:
      {==}
    case Audit{} Allow{} Deny{} True{}:
      {==}
    case Audit{} Audit{} Allow{} False{}:
      {==}
    case Audit{} Audit{} Allow{} True{}:
      {==}
    case Audit{} Audit{} Audit{} False{}:
      {==}
    case Audit{} Audit{} Audit{} True{}:
      {==}
    case Audit{} Audit{} Deny{} False{}:
      {==}
    case Audit{} Audit{} Deny{} True{}:
      {==}
    case Audit{} Deny{} Allow{} False{}:
      {==}
    case Audit{} Deny{} Allow{} True{}:
      {==}
    case Audit{} Deny{} Audit{} False{}:
      {==}
    case Audit{} Deny{} Audit{} True{}:
      {==}
    case Audit{} Deny{} Deny{} False{}:
      {==}
    case Audit{} Deny{} Deny{} True{}:
      {==}
    case Deny{} Allow{} Allow{} False{}:
      {==}
    case Deny{} Allow{} Allow{} True{}:
      absurd({implies(Bool.or(eq(Deny{}, Allow{}), True{}), le(Deny{}, merge(Allow{}, Allow{}))) == True{} : Bool}, ih)
    case Deny{} Allow{} Audit{} False{}:
      {==}
    case Deny{} Allow{} Audit{} True{}:
      absurd({implies(Bool.or(eq(Deny{}, Allow{}), True{}), le(Deny{}, merge(Allow{}, Audit{}))) == True{} : Bool}, ih)
    case Deny{} Allow{} Deny{} False{}:
      {==}
    case Deny{} Allow{} Deny{} True{}:
      {==}
    case Deny{} Audit{} Allow{} False{}:
      {==}
    case Deny{} Audit{} Allow{} True{}:
      absurd({implies(Bool.or(eq(Deny{}, Audit{}), True{}), le(Deny{}, merge(Audit{}, Allow{}))) == True{} : Bool}, ih)
    case Deny{} Audit{} Audit{} False{}:
      {==}
    case Deny{} Audit{} Audit{} True{}:
      absurd({implies(Bool.or(eq(Deny{}, Audit{}), True{}), le(Deny{}, merge(Audit{}, Audit{}))) == True{} : Bool}, ih)
    case Deny{} Audit{} Deny{} False{}:
      {==}
    case Deny{} Audit{} Deny{} True{}:
      {==}
    case Deny{} Deny{} Allow{} False{}:
      {==}
    case Deny{} Deny{} Allow{} True{}:
      {==}
    case Deny{} Deny{} Audit{} False{}:
      {==}
    case Deny{} Deny{} Audit{} True{}:
      {==}
    case Deny{} Deny{} Deny{} False{}:
      {==}
    case Deny{} Deny{} Deny{} True{}:
      {==}

def dom_one_step(+x: Decision, +h: Decision, +d: Decision, +m: Bool, ih: {implies(m, le(x, d)) == True{} : Bool}) -> {implies(Bool.or(le(x, h), m), le(x, merge(h, d))) == True{} : Bool}:
  match x h d m:
    case Allow{} Allow{} Allow{} False{}:
      {==}
    case Allow{} Allow{} Allow{} True{}:
      {==}
    case Allow{} Allow{} Audit{} False{}:
      {==}
    case Allow{} Allow{} Audit{} True{}:
      {==}
    case Allow{} Allow{} Deny{} False{}:
      {==}
    case Allow{} Allow{} Deny{} True{}:
      {==}
    case Allow{} Audit{} Allow{} False{}:
      {==}
    case Allow{} Audit{} Allow{} True{}:
      {==}
    case Allow{} Audit{} Audit{} False{}:
      {==}
    case Allow{} Audit{} Audit{} True{}:
      {==}
    case Allow{} Audit{} Deny{} False{}:
      {==}
    case Allow{} Audit{} Deny{} True{}:
      {==}
    case Allow{} Deny{} Allow{} False{}:
      {==}
    case Allow{} Deny{} Allow{} True{}:
      {==}
    case Allow{} Deny{} Audit{} False{}:
      {==}
    case Allow{} Deny{} Audit{} True{}:
      {==}
    case Allow{} Deny{} Deny{} False{}:
      {==}
    case Allow{} Deny{} Deny{} True{}:
      {==}
    case Audit{} Allow{} Allow{} False{}:
      {==}
    case Audit{} Allow{} Allow{} True{}:
      absurd({implies(Bool.or(le(Audit{}, Allow{}), True{}), le(Audit{}, merge(Allow{}, Allow{}))) == True{} : Bool}, ih)
    case Audit{} Allow{} Audit{} False{}:
      {==}
    case Audit{} Allow{} Audit{} True{}:
      {==}
    case Audit{} Allow{} Deny{} False{}:
      {==}
    case Audit{} Allow{} Deny{} True{}:
      {==}
    case Audit{} Audit{} Allow{} False{}:
      {==}
    case Audit{} Audit{} Allow{} True{}:
      {==}
    case Audit{} Audit{} Audit{} False{}:
      {==}
    case Audit{} Audit{} Audit{} True{}:
      {==}
    case Audit{} Audit{} Deny{} False{}:
      {==}
    case Audit{} Audit{} Deny{} True{}:
      {==}
    case Audit{} Deny{} Allow{} False{}:
      {==}
    case Audit{} Deny{} Allow{} True{}:
      {==}
    case Audit{} Deny{} Audit{} False{}:
      {==}
    case Audit{} Deny{} Audit{} True{}:
      {==}
    case Audit{} Deny{} Deny{} False{}:
      {==}
    case Audit{} Deny{} Deny{} True{}:
      {==}
    case Deny{} Allow{} Allow{} False{}:
      {==}
    case Deny{} Allow{} Allow{} True{}:
      absurd({implies(Bool.or(le(Deny{}, Allow{}), True{}), le(Deny{}, merge(Allow{}, Allow{}))) == True{} : Bool}, ih)
    case Deny{} Allow{} Audit{} False{}:
      {==}
    case Deny{} Allow{} Audit{} True{}:
      absurd({implies(Bool.or(le(Deny{}, Allow{}), True{}), le(Deny{}, merge(Allow{}, Audit{}))) == True{} : Bool}, ih)
    case Deny{} Allow{} Deny{} False{}:
      {==}
    case Deny{} Allow{} Deny{} True{}:
      {==}
    case Deny{} Audit{} Allow{} False{}:
      {==}
    case Deny{} Audit{} Allow{} True{}:
      absurd({implies(Bool.or(le(Deny{}, Audit{}), True{}), le(Deny{}, merge(Audit{}, Allow{}))) == True{} : Bool}, ih)
    case Deny{} Audit{} Audit{} False{}:
      {==}
    case Deny{} Audit{} Audit{} True{}:
      absurd({implies(Bool.or(le(Deny{}, Audit{}), True{}), le(Deny{}, merge(Audit{}, Audit{}))) == True{} : Bool}, ih)
    case Deny{} Audit{} Deny{} False{}:
      {==}
    case Deny{} Audit{} Deny{} True{}:
      {==}
    case Deny{} Deny{} Allow{} False{}:
      {==}
    case Deny{} Deny{} Allow{} True{}:
      {==}
    case Deny{} Deny{} Audit{} False{}:
      {==}
    case Deny{} Deny{} Audit{} True{}:
      {==}
    case Deny{} Deny{} Deny{} False{}:
      {==}
    case Deny{} Deny{} Deny{} True{}:
      {==}

law mem_le_fold:
  for +xs: +List<Decision>
  for +x: Decision
  {implies(mem(x, xs), le(x, fold(xs))) == True{} : Bool}
def mem_le_fold(xs, x):
  match xs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      mem_step(x, h, fold(t), mem(x, t), mem_le_fold(t, x))

law dominated_one_bound:
  for +ys: +List<Decision>
  for +x: Decision
  {implies(dominated_one(x, ys), le(x, fold(ys))) == True{} : Bool}
def dominated_one_bound(ys, x):
  match ys:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      dom_one_step(x, h, fold(t), dominated_one(x, t), dominated_one_bound(t, x))

def dom_fold_step(+a: Decision, +b: Decision, +c: Decision, +p: Bool, +q: Bool, hp: {implies(p, le(a, c)) == True{} : Bool}, hq: {implies(q, le(b, c)) == True{} : Bool}) -> {implies(Bool.and(p, q), le(merge(a, b), c)) == True{} : Bool}:
  match a b c p q:
    case Allow{} Allow{} Allow{} False{} False{}:
      {==}
    case Allow{} Allow{} Allow{} False{} True{}:
      {==}
    case Allow{} Allow{} Allow{} True{} False{}:
      {==}
    case Allow{} Allow{} Allow{} True{} True{}:
      {==}
    case Allow{} Allow{} Audit{} False{} False{}:
      {==}
    case Allow{} Allow{} Audit{} False{} True{}:
      {==}
    case Allow{} Allow{} Audit{} True{} False{}:
      {==}
    case Allow{} Allow{} Audit{} True{} True{}:
      {==}
    case Allow{} Allow{} Deny{} False{} False{}:
      {==}
    case Allow{} Allow{} Deny{} False{} True{}:
      {==}
    case Allow{} Allow{} Deny{} True{} False{}:
      {==}
    case Allow{} Allow{} Deny{} True{} True{}:
      {==}
    case Allow{} Audit{} Allow{} False{} False{}:
      {==}
    case Allow{} Audit{} Allow{} False{} True{}:
      {==}
    case Allow{} Audit{} Allow{} True{} False{}:
      {==}
    case Allow{} Audit{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Allow{}, Audit{}), Allow{})) == True{} : Bool}, hq)
    case Allow{} Audit{} Audit{} False{} False{}:
      {==}
    case Allow{} Audit{} Audit{} False{} True{}:
      {==}
    case Allow{} Audit{} Audit{} True{} False{}:
      {==}
    case Allow{} Audit{} Audit{} True{} True{}:
      {==}
    case Allow{} Audit{} Deny{} False{} False{}:
      {==}
    case Allow{} Audit{} Deny{} False{} True{}:
      {==}
    case Allow{} Audit{} Deny{} True{} False{}:
      {==}
    case Allow{} Audit{} Deny{} True{} True{}:
      {==}
    case Allow{} Deny{} Allow{} False{} False{}:
      {==}
    case Allow{} Deny{} Allow{} False{} True{}:
      {==}
    case Allow{} Deny{} Allow{} True{} False{}:
      {==}
    case Allow{} Deny{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Allow{}, Deny{}), Allow{})) == True{} : Bool}, hq)
    case Allow{} Deny{} Audit{} False{} False{}:
      {==}
    case Allow{} Deny{} Audit{} False{} True{}:
      {==}
    case Allow{} Deny{} Audit{} True{} False{}:
      {==}
    case Allow{} Deny{} Audit{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Allow{}, Deny{}), Audit{})) == True{} : Bool}, hq)
    case Allow{} Deny{} Deny{} False{} False{}:
      {==}
    case Allow{} Deny{} Deny{} False{} True{}:
      {==}
    case Allow{} Deny{} Deny{} True{} False{}:
      {==}
    case Allow{} Deny{} Deny{} True{} True{}:
      {==}
    case Audit{} Allow{} Allow{} False{} False{}:
      {==}
    case Audit{} Allow{} Allow{} False{} True{}:
      {==}
    case Audit{} Allow{} Allow{} True{} False{}:
      {==}
    case Audit{} Allow{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Audit{}, Allow{}), Allow{})) == True{} : Bool}, hp)
    case Audit{} Allow{} Audit{} False{} False{}:
      {==}
    case Audit{} Allow{} Audit{} False{} True{}:
      {==}
    case Audit{} Allow{} Audit{} True{} False{}:
      {==}
    case Audit{} Allow{} Audit{} True{} True{}:
      {==}
    case Audit{} Allow{} Deny{} False{} False{}:
      {==}
    case Audit{} Allow{} Deny{} False{} True{}:
      {==}
    case Audit{} Allow{} Deny{} True{} False{}:
      {==}
    case Audit{} Allow{} Deny{} True{} True{}:
      {==}
    case Audit{} Audit{} Allow{} False{} False{}:
      {==}
    case Audit{} Audit{} Allow{} False{} True{}:
      {==}
    case Audit{} Audit{} Allow{} True{} False{}:
      {==}
    case Audit{} Audit{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Audit{}, Audit{}), Allow{})) == True{} : Bool}, hp)
    case Audit{} Audit{} Audit{} False{} False{}:
      {==}
    case Audit{} Audit{} Audit{} False{} True{}:
      {==}
    case Audit{} Audit{} Audit{} True{} False{}:
      {==}
    case Audit{} Audit{} Audit{} True{} True{}:
      {==}
    case Audit{} Audit{} Deny{} False{} False{}:
      {==}
    case Audit{} Audit{} Deny{} False{} True{}:
      {==}
    case Audit{} Audit{} Deny{} True{} False{}:
      {==}
    case Audit{} Audit{} Deny{} True{} True{}:
      {==}
    case Audit{} Deny{} Allow{} False{} False{}:
      {==}
    case Audit{} Deny{} Allow{} False{} True{}:
      {==}
    case Audit{} Deny{} Allow{} True{} False{}:
      {==}
    case Audit{} Deny{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Audit{}, Deny{}), Allow{})) == True{} : Bool}, hp)
    case Audit{} Deny{} Audit{} False{} False{}:
      {==}
    case Audit{} Deny{} Audit{} False{} True{}:
      {==}
    case Audit{} Deny{} Audit{} True{} False{}:
      {==}
    case Audit{} Deny{} Audit{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Audit{}, Deny{}), Audit{})) == True{} : Bool}, hq)
    case Audit{} Deny{} Deny{} False{} False{}:
      {==}
    case Audit{} Deny{} Deny{} False{} True{}:
      {==}
    case Audit{} Deny{} Deny{} True{} False{}:
      {==}
    case Audit{} Deny{} Deny{} True{} True{}:
      {==}
    case Deny{} Allow{} Allow{} False{} False{}:
      {==}
    case Deny{} Allow{} Allow{} False{} True{}:
      {==}
    case Deny{} Allow{} Allow{} True{} False{}:
      {==}
    case Deny{} Allow{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Deny{}, Allow{}), Allow{})) == True{} : Bool}, hp)
    case Deny{} Allow{} Audit{} False{} False{}:
      {==}
    case Deny{} Allow{} Audit{} False{} True{}:
      {==}
    case Deny{} Allow{} Audit{} True{} False{}:
      {==}
    case Deny{} Allow{} Audit{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Deny{}, Allow{}), Audit{})) == True{} : Bool}, hp)
    case Deny{} Allow{} Deny{} False{} False{}:
      {==}
    case Deny{} Allow{} Deny{} False{} True{}:
      {==}
    case Deny{} Allow{} Deny{} True{} False{}:
      {==}
    case Deny{} Allow{} Deny{} True{} True{}:
      {==}
    case Deny{} Audit{} Allow{} False{} False{}:
      {==}
    case Deny{} Audit{} Allow{} False{} True{}:
      {==}
    case Deny{} Audit{} Allow{} True{} False{}:
      {==}
    case Deny{} Audit{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Deny{}, Audit{}), Allow{})) == True{} : Bool}, hp)
    case Deny{} Audit{} Audit{} False{} False{}:
      {==}
    case Deny{} Audit{} Audit{} False{} True{}:
      {==}
    case Deny{} Audit{} Audit{} True{} False{}:
      {==}
    case Deny{} Audit{} Audit{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Deny{}, Audit{}), Audit{})) == True{} : Bool}, hp)
    case Deny{} Audit{} Deny{} False{} False{}:
      {==}
    case Deny{} Audit{} Deny{} False{} True{}:
      {==}
    case Deny{} Audit{} Deny{} True{} False{}:
      {==}
    case Deny{} Audit{} Deny{} True{} True{}:
      {==}
    case Deny{} Deny{} Allow{} False{} False{}:
      {==}
    case Deny{} Deny{} Allow{} False{} True{}:
      {==}
    case Deny{} Deny{} Allow{} True{} False{}:
      {==}
    case Deny{} Deny{} Allow{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Deny{}, Deny{}), Allow{})) == True{} : Bool}, hp)
    case Deny{} Deny{} Audit{} False{} False{}:
      {==}
    case Deny{} Deny{} Audit{} False{} True{}:
      {==}
    case Deny{} Deny{} Audit{} True{} False{}:
      {==}
    case Deny{} Deny{} Audit{} True{} True{}:
      absurd({implies(Bool.and(True{}, True{}), le(merge(Deny{}, Deny{}), Audit{})) == True{} : Bool}, hp)
    case Deny{} Deny{} Deny{} False{} False{}:
      {==}
    case Deny{} Deny{} Deny{} False{} True{}:
      {==}
    case Deny{} Deny{} Deny{} True{} False{}:
      {==}
    case Deny{} Deny{} Deny{} True{} True{}:
      {==}

law fold_le_of_dominated:
  for +xs: +List<Decision>
  for +ys: +List<Decision>
  {implies(dominated(xs, ys), le(fold(xs), fold(ys))) == True{} : Bool}
def fold_le_of_dominated(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      dom_fold_step(h, fold(t), fold(ys), dominated_one(h, ys), dominated(t, ys), dominated_one_bound(ys, h), fold_le_of_dominated(t, ys))

def deny_le_eq(+d: Decision) -> {le(Deny{}, d) == is_deny(d) : Bool}:
  match d:
    case Allow{}:
      {==}
    case Audit{}:
      {==}
    case Deny{}:
      {==}

law deny_wins:
  for +xs: +List<Decision>
  for h: {mem(Deny{}, xs) == True{} : Bool}
  {is_deny(fold(xs)) == True{} : Bool}
def deny_wins(xs, h):
  %deny_le_eq(fold(xs)) : {_ == True{} : Bool}
  mp(mem(Deny{}, xs), le(Deny{}, fold(xs)), mem_le_fold(xs, Deny{}), h)

# Base has no List.Perm. This witness has Lean's nil/cons/swap/trans constructors;
# endpoints are computed rather than datatype indices. Trans carries the exact
# endpoint equality, so it cannot compose unrelated lists. No bounded enumeration.
type Permutation<-A: Data> is Data:
  PermNil{}
  PermCons{head: A, tail: Permutation<A>}
  PermSwap{a: A, b: A, tail: +List<A>}
  PermTrans{left: Permutation<A>, right: Permutation<A>, bridge: {perm_dst(A, left) == perm_src(A, right) : +List<A>}}

def perm_src(-A: Data, p: Permutation<A>) -> +List<A>:
  match p:
    case PermNil{}:
      []
    case PermCons{x, t}:
      x <> perm_src(A, t)
    case PermSwap{x, y, t}:
      x <> y <> t
    case PermTrans{p, q, b}:
      perm_src(A, p)

def perm_dst(-A: Data, p: Permutation<A>) -> +List<A>:
  match p:
    case PermNil{}:
      []
    case PermCons{x, t}:
      x <> perm_dst(A, t)
    case PermSwap{x, y, t}:
      y <> x <> t
    case PermTrans{p, q, b}:
      perm_dst(A, q)

law swap_fold:
  for +x: Decision
  for +y: Decision
  for +c: Decision
  {merge(x, merge(y, c)) == merge(y, merge(x, c)) : Decision}
def swap_fold(x, y, c):
  Equal.trans(Decision, merge(x, merge(y, c)), merge(merge(x, y), c), merge(y, merge(x, c)), Equal.sym(Decision, merge(merge(x, y), c), merge(x, merge(y, c)), merge_assoc(x, y, c)), Equal.trans(Decision, merge(merge(x, y), c), merge(merge(y, x), c), merge(y, merge(x, c)), Equal.cong(Decision, Decision, d => merge(d, c), merge(x, y), merge(y, x), merge_comm(x, y)), merge_assoc(y, x, c)))

# Rule/operation iteration order is independent of map ordering.
law fold_perm:
  for +p: Permutation<Decision>
  {fold(perm_src(Decision, p)) == fold(perm_dst(Decision, p)) : Decision}
def fold_perm(p):
  match p:
    case PermNil{}:
      {==}
    case PermCons{+x, +t}:
      %fold_perm(t) : {merge(x, fold(perm_src(Decision, t))) == merge(x, _) : Decision}
      {==}
    case PermSwap{+x, +y, +t}:
      swap_fold(x, y, fold(t))
    case PermTrans{+p, +q, b}:
      Equal.trans(Decision, fold(perm_src(Decision, p)), fold(perm_dst(Decision, p)), fold(perm_dst(Decision, q)), fold_perm(p), Equal.trans(Decision, fold(perm_dst(Decision, p)), fold(perm_src(Decision, q)), fold(perm_dst(Decision, q)), Equal.cong(+List<Decision>, Decision, xs => fold(xs), perm_dst(Decision, p), perm_src(Decision, q), b), fold_perm(q)))
