# Port of Ghok/Hooks.lean: decision side of internal/hooks/runner.go.
# Scheduler, dependsOn, wait, cycles, duplicate IDs and skip-after-denial remain
# out of model, exactly as in Lean. All results, including runner diagnostics,
# pass through classify (runner.go:100-103). Failed covers timeout, canceled,
# invalid response, exit and spawn. Prop membership / iff use Bool equalities.
# Hypotheses fixing fields are normalized to the corresponding constructors.
# Base has Empty/Either/Sigma/Maybe; Policy defines a checked permutation
# witness because Base has no List.Perm.
import Base
import ./policy.bend as P

type HDecision is Data:
  Abstain{}
  Allow{}
  Deny{}
type Outcome is Data:
  Ok{}
  Skipped{}
  Failed{}
type OnError is Data:
  ErrorAllow{}
  Ignore{}
  ErrorDeny{}
type Phase is Data:
  Pre{}
  Post{}
type Hook is Data:
  Hook{on_error: OnError, continue_on_error: Bool}
type HookResult is Data:
  HookResult{outcome: Outcome, decision: HDecision}
type Classified is Data:
  Classified{decision: HDecision, continued: Bool}

def is_deny(d: HDecision) -> Bool:
  match d:
    case Deny{}:
      True{}
    case _:
      False{}

def is_allow(d: HDecision) -> Bool:
  match d:
    case Allow{}:
      True{}
    case _:
      False{}

def is_abstain(d: HDecision) -> Bool:
  match d:
    case Abstain{}:
      True{}
    case _:
      False{}

def is_pre(ph: Phase) -> Bool:
  match ph:
    case Pre{}:
      True{}
    case Post{}:
      False{}

def is_ok(r: HookResult) -> Bool:
  match r:
    case HookResult{Ok{}, d}:
      True{}
    case _:
      False{}

# runner.go:72. Default onError maps to ErrorDeny (including unknown strings).
def effective_failure(h: Hook) -> HDecision:
  match h:
    case Hook{ErrorAllow{}, c}:
      Allow{}
    case Hook{Ignore{}, c}:
      Abstain{}
    case Hook{ErrorDeny{}, c}:
      Deny{}

def effective_pre(h: Hook, r: HookResult) -> HDecision:
  match r:
    case HookResult{Ok{}, d}:
      d
    case HookResult{Skipped{}, d}:
      Abstain{}
    case HookResult{Failed{}, d}:
      effective_failure(h)

def effective(ph: Phase, h: Hook, r: HookResult) -> HDecision:
  match ph:
    case Post{}:
      Abstain{}
    case Pre{}:
      effective_pre(h, r)

# runner.go:92; the effective decision and continuedOnError.
def classify_effective(ph: Phase, c: Bool, +e: HDecision) -> Classified:
  Bool.pick(Classified, Bool.and(is_pre(ph), Bool.and(c, is_deny(e))), Classified{Abstain{}, True{}}, Classified{e, False{}})

def classify(+ph: Phase, +h: Hook, r: HookResult) -> Classified:
  Hook{err, c} = h
  classify_effective(ph, c, effective(ph, h, r))

def eff(c: Classified) -> HDecision:
  Classified{d, b} = c
  d

def continued(c: Classified) -> Bool:
  Classified{d, b} = c
  b

def continue_flag(h: Hook) -> Bool:
  Hook{err, c} = h
  c

# runner.go:280-298. Message selection is not part of either model.
def aggregate_step(ph: Phase, +e: HDecision, +rest: HDecision) -> HDecision:
  Bool.pick(HDecision, Bool.and(is_pre(ph), is_deny(e)), Deny{}, Bool.pick(HDecision, Bool.and(is_abstain(rest), is_allow(e)), Allow{}, rest))

def aggregate(+ph: Phase, es: +List<HDecision>) -> HDecision:
  match es:
    case Nil{}:
      Abstain{}
    case Con{e, t}:
      aggregate_step(ph, e, aggregate(ph, t))

def has_deny(es: +List<HDecision>) -> Bool:
  match es:
    case Nil{}:
      False{}
    case Con{e, t}:
      Bool.or(is_deny(e), has_deny(t))

def has_allow(es: +List<HDecision>) -> Bool:
  match es:
    case Nil{}:
      False{}
    case Con{e, t}:
      Bool.or(is_allow(e), has_allow(t))

def spec_bits(ph: Phase, hd: Bool, ha: Bool) -> HDecision:
  Bool.pick(HDecision, Bool.and(is_pre(ph), hd), Deny{}, Bool.pick(HDecision, ha, Allow{}, Abstain{}))

def spec(ph: Phase, +es: +List<HDecision>) -> HDecision:
  spec_bits(ph, has_deny(es), has_allow(es))

law classify_post:
  for +h: Hook
  for +r: HookResult
  {eff(classify(Post{}, h, r)) == Abstain{} : HDecision}
def classify_post(h, r):
  match h:
    case Hook{err, c}:
      {==}

# ho/hc/hr in Lean normalized into Hook and HookResult below.
law classify_failed_deny:
  for +d: HDecision
  {eff(classify(Pre{}, Hook{ErrorDeny{}, False{}}, HookResult{Failed{}, d})) == Deny{} : HDecision}
def classify_failed_deny(d):
  {==}

law classify_ok_deny:
  for +err: OnError
  {eff(classify(Pre{}, Hook{err, False{}}, HookResult{Ok{}, Deny{}})) == Deny{} : HDecision}
def classify_ok_deny(err):
  {==}

law continue_effective:
  for +e: HDecision
  {is_deny(eff(classify_effective(Pre{}, True{}, e))) == False{} : Bool}

def continue_effective(e):
  match e:
    case Abstain{}:
      {==}
    case Allow{}:
      {==}
    case Deny{}:
      {==}

law classify_continue:
  for +ph: Phase
  for +err: OnError
  for +r: HookResult
  {is_deny(eff(classify(ph, Hook{err, True{}}, r))) == False{} : Bool}
def classify_continue(ph, err, r):
  match ph:
    case Pre{}:
      continue_effective(effective(Pre{}, Hook{err, True{}}, r))
    case Post{}:
      {==}

law continued_effective:
  for +ph: Phase
  for +c: Bool
  for +e: HDecision
  {continued(classify_effective(ph, c, e)) == Bool.and(is_pre(ph), Bool.and(c, is_deny(e))) : Bool}

def continued_effective(ph, c, e):
  match ph c e:
    case Pre{} False{} Abstain{}:
      {==}
    case Pre{} False{} Allow{}:
      {==}
    case Pre{} False{} Deny{}:
      {==}
    case Pre{} True{} Abstain{}:
      {==}
    case Pre{} True{} Allow{}:
      {==}
    case Pre{} True{} Deny{}:
      {==}
    case Post{} False{} Abstain{}:
      {==}
    case Post{} False{} Allow{}:
      {==}
    case Post{} False{} Deny{}:
      {==}
    case Post{} True{} Abstain{}:
      {==}
    case Post{} True{} Allow{}:
      {==}
    case Post{} True{} Deny{}:
      {==}

law classify_continued_iff:
  for +ph: Phase
  for +h: Hook
  for +r: HookResult
  {continued(classify(ph, h, r)) == Bool.and(is_pre(ph), Bool.and(continue_flag(h), is_deny(effective(ph, h, r)))) : Bool}
def classify_continued_iff(ph, h, r):
  match h:
    case Hook{+err, +c}:
      continued_effective(ph, c, effective(ph, Hook{err, c}, r))

law classify_skipped:
  for +ph: Phase
  for +h: Hook
  for +d: HDecision
  {eff(classify(ph, h, HookResult{Skipped{}, d})) == Abstain{} : HDecision}
def classify_skipped(ph, h, d):
  match ph h:
    case Pre{} Hook{err, False{}}:
      {==}
    case Pre{} Hook{err, True{}}:
      {==}
    case Post{} Hook{err, c}:
      {==}

law spec_step:
  for +ph: Phase
  for +e: HDecision
  for +hd: Bool
  for +ha: Bool
  {aggregate_step(ph, e, spec_bits(ph, hd, ha)) == spec_bits(ph, Bool.or(is_deny(e), hd), Bool.or(is_allow(e), ha)) : HDecision}

def spec_step(ph, e, hd, ha):
  match ph e hd ha:
    case Pre{} Abstain{} False{} False{}:
      {==}
    case Pre{} Abstain{} False{} True{}:
      {==}
    case Pre{} Abstain{} True{} False{}:
      {==}
    case Pre{} Abstain{} True{} True{}:
      {==}
    case Pre{} Allow{} False{} False{}:
      {==}
    case Pre{} Allow{} False{} True{}:
      {==}
    case Pre{} Allow{} True{} False{}:
      {==}
    case Pre{} Allow{} True{} True{}:
      {==}
    case Pre{} Deny{} False{} False{}:
      {==}
    case Pre{} Deny{} False{} True{}:
      {==}
    case Pre{} Deny{} True{} False{}:
      {==}
    case Pre{} Deny{} True{} True{}:
      {==}
    case Post{} Abstain{} False{} False{}:
      {==}
    case Post{} Abstain{} False{} True{}:
      {==}
    case Post{} Abstain{} True{} False{}:
      {==}
    case Post{} Abstain{} True{} True{}:
      {==}
    case Post{} Allow{} False{} False{}:
      {==}
    case Post{} Allow{} False{} True{}:
      {==}
    case Post{} Allow{} True{} False{}:
      {==}
    case Post{} Allow{} True{} True{}:
      {==}
    case Post{} Deny{} False{} False{}:
      {==}
    case Post{} Deny{} False{} True{}:
      {==}
    case Post{} Deny{} True{} False{}:
      {==}
    case Post{} Deny{} True{} True{}:
      {==}

law aggregate_eq_spec:
  for +es: +List<HDecision>
  for +ph: Phase
  {aggregate(ph, es) == spec(ph, es) : HDecision}
def aggregate_eq_spec(es, ph):
  match es ph:
    case Nil{} Pre{}:
      {==}
    case Nil{} Post{}:
      {==}
    case Con{+e, +t} ph:
      %Equal.sym(HDecision, aggregate(ph, t), spec(ph, t), aggregate_eq_spec(t, ph)) : {aggregate_step(ph, e, _) == spec(ph, e <> t) : HDecision}
      spec_step(ph, e, has_deny(t), has_allow(t))

law post_spec_ne:
  for +hd: Bool
  for +ha: Bool
  {is_deny(spec_bits(Post{}, hd, ha)) == False{} : Bool}

def post_spec_ne(hd, ha):
  match hd ha:
    case False{} False{}:
      {==}
    case False{} True{}:
      {==}
    case True{} False{}:
      {==}
    case True{} True{}:
      {==}

law pre_spec_deny:
  for +hd: Bool
  for +ha: Bool
  {is_deny(spec_bits(Pre{}, hd, ha)) == hd : Bool}

def pre_spec_deny(hd, ha):
  match hd ha:
    case False{} False{}:
      {==}
    case False{} True{}:
      {==}
    case True{} False{}:
      {==}
    case True{} True{}:
      {==}

law aggregate_post_ne_deny:
  for +es: +List<HDecision>
  {is_deny(aggregate(Post{}, es)) == False{} : Bool}
def aggregate_post_ne_deny(es):
  %Equal.sym(HDecision, aggregate(Post{}, es), spec(Post{}, es), aggregate_eq_spec(es, Post{})) : {is_deny(_) == False{} : Bool}
  post_spec_ne(has_deny(es), has_allow(es))

law aggregate_pre_deny_iff:
  for +es: +List<HDecision>
  {is_deny(aggregate(Pre{}, es)) == has_deny(es) : Bool}
def aggregate_pre_deny_iff(es):
  %Equal.sym(HDecision, aggregate(Pre{}, es), spec(Pre{}, es), aggregate_eq_spec(es, Pre{})) : {is_deny(_) == has_deny(es) : Bool}
  pre_spec_deny(has_deny(es), has_allow(es))

# Membership inclusion may be specialized to the deny member: this is equivalent
# for this conclusion and makes a stronger theorem than Lean's all-member premise.
law aggregate_pre_deny_mono:
  for +es: +List<HDecision>
  for +es2: +List<HDecision>
  for inc: {P.implies(has_deny(es), has_deny(es2)) == True{} : Bool}
  for hd: {is_deny(aggregate(Pre{}, es)) == True{} : Bool}
  {is_deny(aggregate(Pre{}, es2)) == True{} : Bool}
def aggregate_pre_deny_mono(es, es2, inc, hd):
  %Equal.sym(Bool, is_deny(aggregate(Pre{}, es2)), has_deny(es2), aggregate_pre_deny_iff(es2)) : {_ == True{} : Bool}
  P.mp(has_deny(es), has_deny(es2), inc, Equal.trans(Bool, has_deny(es), is_deny(aggregate(Pre{}, es)), True{}, Equal.sym(Bool, is_deny(aggregate(Pre{}, es)), has_deny(es), aggregate_pre_deny_iff(es)), hd))

law aggregate_swap:
  for +ph: Phase
  for +x: HDecision
  for +y: HDecision
  for +c: HDecision
  {aggregate_step(ph, x, aggregate_step(ph, y, c)) == aggregate_step(ph, y, aggregate_step(ph, x, c)) : HDecision}

def aggregate_swap(ph, x, y, c):
  match ph x y c:
    case Pre{} Abstain{} Abstain{} Abstain{}:
      {==}
    case Pre{} Abstain{} Abstain{} Allow{}:
      {==}
    case Pre{} Abstain{} Abstain{} Deny{}:
      {==}
    case Pre{} Abstain{} Allow{} Abstain{}:
      {==}
    case Pre{} Abstain{} Allow{} Allow{}:
      {==}
    case Pre{} Abstain{} Allow{} Deny{}:
      {==}
    case Pre{} Abstain{} Deny{} Abstain{}:
      {==}
    case Pre{} Abstain{} Deny{} Allow{}:
      {==}
    case Pre{} Abstain{} Deny{} Deny{}:
      {==}
    case Pre{} Allow{} Abstain{} Abstain{}:
      {==}
    case Pre{} Allow{} Abstain{} Allow{}:
      {==}
    case Pre{} Allow{} Abstain{} Deny{}:
      {==}
    case Pre{} Allow{} Allow{} Abstain{}:
      {==}
    case Pre{} Allow{} Allow{} Allow{}:
      {==}
    case Pre{} Allow{} Allow{} Deny{}:
      {==}
    case Pre{} Allow{} Deny{} Abstain{}:
      {==}
    case Pre{} Allow{} Deny{} Allow{}:
      {==}
    case Pre{} Allow{} Deny{} Deny{}:
      {==}
    case Pre{} Deny{} Abstain{} Abstain{}:
      {==}
    case Pre{} Deny{} Abstain{} Allow{}:
      {==}
    case Pre{} Deny{} Abstain{} Deny{}:
      {==}
    case Pre{} Deny{} Allow{} Abstain{}:
      {==}
    case Pre{} Deny{} Allow{} Allow{}:
      {==}
    case Pre{} Deny{} Allow{} Deny{}:
      {==}
    case Pre{} Deny{} Deny{} Abstain{}:
      {==}
    case Pre{} Deny{} Deny{} Allow{}:
      {==}
    case Pre{} Deny{} Deny{} Deny{}:
      {==}
    case Post{} Abstain{} Abstain{} Abstain{}:
      {==}
    case Post{} Abstain{} Abstain{} Allow{}:
      {==}
    case Post{} Abstain{} Abstain{} Deny{}:
      {==}
    case Post{} Abstain{} Allow{} Abstain{}:
      {==}
    case Post{} Abstain{} Allow{} Allow{}:
      {==}
    case Post{} Abstain{} Allow{} Deny{}:
      {==}
    case Post{} Abstain{} Deny{} Abstain{}:
      {==}
    case Post{} Abstain{} Deny{} Allow{}:
      {==}
    case Post{} Abstain{} Deny{} Deny{}:
      {==}
    case Post{} Allow{} Abstain{} Abstain{}:
      {==}
    case Post{} Allow{} Abstain{} Allow{}:
      {==}
    case Post{} Allow{} Abstain{} Deny{}:
      {==}
    case Post{} Allow{} Allow{} Abstain{}:
      {==}
    case Post{} Allow{} Allow{} Allow{}:
      {==}
    case Post{} Allow{} Allow{} Deny{}:
      {==}
    case Post{} Allow{} Deny{} Abstain{}:
      {==}
    case Post{} Allow{} Deny{} Allow{}:
      {==}
    case Post{} Allow{} Deny{} Deny{}:
      {==}
    case Post{} Deny{} Abstain{} Abstain{}:
      {==}
    case Post{} Deny{} Abstain{} Allow{}:
      {==}
    case Post{} Deny{} Abstain{} Deny{}:
      {==}
    case Post{} Deny{} Allow{} Abstain{}:
      {==}
    case Post{} Deny{} Allow{} Allow{}:
      {==}
    case Post{} Deny{} Allow{} Deny{}:
      {==}
    case Post{} Deny{} Deny{} Abstain{}:
      {==}
    case Post{} Deny{} Deny{} Allow{}:
      {==}
    case Post{} Deny{} Deny{} Deny{}:
      {==}

# Completion order does not matter: same permutation witness as Policy.
law aggregate_perm:
  for +p: P.Permutation<HDecision>
  for +ph: Phase
  {aggregate(ph, P.perm_src(HDecision, p)) == aggregate(ph, P.perm_dst(HDecision, p)) : HDecision}
def aggregate_perm(p, ph):
  match p:
    case P.PermNil{}:
      {==}
    case P.PermCons{+x, +t}:
      %aggregate_perm(t, ph) : {aggregate_step(ph, x, aggregate(ph, P.perm_src(HDecision, t))) == aggregate_step(ph, x, _) : HDecision}
      {==}
    case P.PermSwap{+x, +y, +t}:
      aggregate_swap(ph, x, y, aggregate(ph, t))
    case P.PermTrans{+p, +q, b}:
      Equal.trans(HDecision, aggregate(ph, P.perm_src(HDecision, p)), aggregate(ph, P.perm_dst(HDecision, p)), aggregate(ph, P.perm_dst(HDecision, q)), aggregate_perm(p, ph), Equal.trans(HDecision, aggregate(ph, P.perm_dst(HDecision, p)), aggregate(ph, P.perm_src(HDecision, q)), aggregate(ph, P.perm_dst(HDecision, q)), Equal.cong(+List<HDecision>, HDecision, xs => aggregate(ph, xs), P.perm_dst(HDecision, p), P.perm_src(HDecision, q), b), aggregate_perm(q, ph)))
