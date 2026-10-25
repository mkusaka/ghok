# Port of Ghok/Config.lean: internal/config/project.go:322, local.go:84.
# Both inputs are post-ApplyDefaults (load.go:219). Subject is fixed; transforms,
# hook scheduling, Validate and duplicate IDs are out of model, as in Lean.
# Dom is Bool forall/exists over contributions (kinds deliberately ignored), NOT
# merely an order on folds. Base has Sigma/Either/Empty/Maybe; Bool is convenient.
import Base
import ./policy.bend as P
import ./match.bend as M

type Presence is Data:
  Presence{mode: Bool, unknown_mutation: Bool, unknown_input: Bool, protected_unknown: Bool}

# project.go:351-367: a lower rank is ignored with a note.
def raise(present: Bool, +cur: P.Decision, +new: P.Decision) -> P.Decision:
  Bool.pick(P.Decision, Bool.and(present, P.le(cur, new)), new, cur)

# project.go:343-349: enforce -> observe is ignored with a note.
def overlay_mode(present: Bool, +cur: P.Mode, +new: P.Mode) -> P.Mode:
  Bool.pick(P.Mode, Bool.and(present, Bool.not(Bool.and(P.is_enforce(cur), Bool.not(P.is_enforce(new))))), new, cur)

def overlay(pr: Presence, c: P.Policy, p: P.Policy) -> P.Policy:
  Presence{pm, pum, pui, ppu} = pr
  P.Policy{cm, crs, cum, cui, cpu, cps} = c
  P.Policy{nm, nrs, num, nui, npu, nps} = p
  P.Policy{overlay_mode(pm, cm, nm), List.append(&2, P.Rule, crs, nrs), raise(pum, cum, num), raise(pui, cui, nui), raise(ppu, cpu, npu), List.append(&2, String, cps, nps)}

def Dom(xs: +List<P.Contrib>, ys: +List<P.Contrib>) -> Bool:
  P.dominated(P.decisions(xs), P.decisions(ys))

law raise_ge:
  for +pr: Bool
  for +cur: P.Decision
  for +new: P.Decision
  {P.le(cur, raise(pr, cur, new)) == True{} : Bool}

def raise_ge(pr, cur, new):
  match pr cur new:
    case False{} P.Allow{} P.Allow{}:
      {==}
    case False{} P.Allow{} P.Audit{}:
      {==}
    case False{} P.Allow{} P.Deny{}:
      {==}
    case False{} P.Audit{} P.Allow{}:
      {==}
    case False{} P.Audit{} P.Audit{}:
      {==}
    case False{} P.Audit{} P.Deny{}:
      {==}
    case False{} P.Deny{} P.Allow{}:
      {==}
    case False{} P.Deny{} P.Audit{}:
      {==}
    case False{} P.Deny{} P.Deny{}:
      {==}
    case True{} P.Allow{} P.Allow{}:
      {==}
    case True{} P.Allow{} P.Audit{}:
      {==}
    case True{} P.Allow{} P.Deny{}:
      {==}
    case True{} P.Audit{} P.Allow{}:
      {==}
    case True{} P.Audit{} P.Audit{}:
      {==}
    case True{} P.Audit{} P.Deny{}:
      {==}
    case True{} P.Deny{} P.Allow{}:
      {==}
    case True{} P.Deny{} P.Audit{}:
      {==}
    case True{} P.Deny{} P.Deny{}:
      {==}

law overlayMode_ge:
  for +pr: Bool
  for +cur: P.Mode
  for +new: P.Mode
  {Nat.is_le(P.mode_rank(cur), P.mode_rank(overlay_mode(pr, cur, new))) == True{} : Bool}

def overlayMode_ge(pr, cur, new):
  match pr cur new:
    case False{} P.Observe{} P.Observe{}:
      {==}
    case False{} P.Observe{} P.Enforce{}:
      {==}
    case False{} P.Enforce{} P.Observe{}:
      {==}
    case False{} P.Enforce{} P.Enforce{}:
      {==}
    case True{} P.Observe{} P.Observe{}:
      {==}
    case True{} P.Observe{} P.Enforce{}:
      {==}
    case True{} P.Enforce{} P.Observe{}:
      {==}
    case True{} P.Enforce{} P.Enforce{}:
      {==}

law or_assoc:
  for +a: Bool
  for +b: Bool
  for +c: Bool
  {Bool.or(a, Bool.or(b, c)) == Bool.or(Bool.or(a, b), c) : Bool}

def or_assoc(a, b, c):
  match a b c:
    case False{} False{} False{}:
      {==}
    case False{} False{} True{}:
      {==}
    case False{} True{} False{}:
      {==}
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

law and_assoc:
  for +a: Bool
  for +b: Bool
  for +c: Bool
  {Bool.and(a, Bool.and(b, c)) == Bool.and(Bool.and(a, b), c) : Bool}

def and_assoc(a, b, c):
  match a b c:
    case False{} False{} False{}:
      {==}
    case False{} False{} True{}:
      {==}
    case False{} True{} False{}:
      {==}
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

def and_intro(+a: Bool, +b: Bool, ha: {a == True{} : Bool}, hb: {b == True{} : Bool}) -> {Bool.and(a, b) == True{} : Bool}:
  match a:
    case False{}:
      P.absurd({False{} == True{} : Bool}, ha)
    case True{}:
      hb

def or_left(+a: Bool, +b: Bool, ha: {a == True{} : Bool}) -> {Bool.or(a, b) == True{} : Bool}:
  match a:
    case False{}:
      P.absurd({b == True{} : Bool}, ha)
    case True{}:
      {==}

def or_right(+a: Bool, +b: Bool, hb: {b == True{} : Bool}) -> {Bool.or(a, b) == True{} : Bool}:
  match a:
    case False{}:
      hb
    case True{}:
      {==}

law one_append:
  for +a: +List<P.Decision>
  for +b: +List<P.Decision>
  for +x: P.Decision
  {P.dominated_one(x, P.cat(a, b)) == Bool.or(P.dominated_one(x, a), P.dominated_one(x, b)) : Bool}
def one_append(a, b, x):
  match a:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %Equal.sym(Bool, P.dominated_one(x, P.cat(t, b)), Bool.or(P.dominated_one(x, t), P.dominated_one(x, b)), one_append(t, b, x)) : {Bool.or(P.le(x, h), _) == Bool.or(P.dominated_one(x, h <> t), P.dominated_one(x, b)) : Bool}
      or_assoc(P.le(x, h), P.dominated_one(x, t), P.dominated_one(x, b))

law dom_source_append:
  for +a: +List<P.Decision>
  for +b: +List<P.Decision>
  for +ys: +List<P.Decision>
  {P.dominated(P.cat(a, b), ys) == Bool.and(P.dominated(a, ys), P.dominated(b, ys)) : Bool}
def dom_source_append(a, b, ys):
  match a:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %Equal.sym(Bool, P.dominated(P.cat(t, b), ys), Bool.and(P.dominated(t, ys), P.dominated(b, ys)), dom_source_append(t, b, ys)) : {Bool.and(P.dominated_one(h, ys), _) == Bool.and(P.dominated(h <> t, ys), P.dominated(b, ys)) : Bool}
      and_assoc(P.dominated_one(h, ys), P.dominated(t, ys), P.dominated(b, ys))

def one_target_left(+x: P.Decision, +a: +List<P.Decision>, +b: +List<P.Decision>, h: {P.dominated_one(x, a) == True{} : Bool}) -> {P.dominated_one(x, P.cat(a, b)) == True{} : Bool}:
  Equal.trans(Bool, P.dominated_one(x, P.cat(a, b)), Bool.or(P.dominated_one(x, a), P.dominated_one(x, b)), True{}, one_append(a, b, x), or_left(P.dominated_one(x, a), P.dominated_one(x, b), h))

def one_target_right(+x: P.Decision, +a: +List<P.Decision>, +b: +List<P.Decision>, h: {P.dominated_one(x, b) == True{} : Bool}) -> {P.dominated_one(x, P.cat(a, b)) == True{} : Bool}:
  Equal.trans(Bool, P.dominated_one(x, P.cat(a, b)), Bool.or(P.dominated_one(x, a), P.dominated_one(x, b)), True{}, one_append(a, b, x), or_right(P.dominated_one(x, a), P.dominated_one(x, b), h))

law dom_target_left:
  for +xs: +List<P.Decision>
  for +a: +List<P.Decision>
  for +b: +List<P.Decision>
  for +h: {P.dominated(xs, a) == True{} : Bool}
  {P.dominated(xs, P.cat(a, b)) == True{} : Bool}
def dom_target_left(xs, a, b, h):
  match xs:
    case Nil{}:
      {==}
    case Con{+x, +t}:
      and_intro(P.dominated_one(x, P.cat(a, b)), P.dominated(t, P.cat(a, b)), one_target_left(x, a, b, M.and_left(P.dominated_one(x, a), P.dominated(t, a), h)), dom_target_left(t, a, b, M.and_right(P.dominated_one(x, a), P.dominated(t, a), h)))

law dom_target_right:
  for +xs: +List<P.Decision>
  for +a: +List<P.Decision>
  for +b: +List<P.Decision>
  for +h: {P.dominated(xs, b) == True{} : Bool}
  {P.dominated(xs, P.cat(a, b)) == True{} : Bool}
def dom_target_right(xs, a, b, h):
  match xs:
    case Nil{}:
      {==}
    case Con{+x, +t}:
      and_intro(P.dominated_one(x, P.cat(a, b)), P.dominated(t, P.cat(a, b)), one_target_right(x, a, b, M.and_left(P.dominated_one(x, b), P.dominated(t, b), h)), dom_target_right(t, a, b, M.and_right(P.dominated_one(x, b), P.dominated(t, b), h)))

def one_self(+a: P.Decision, +t: +List<P.Decision>) -> {P.dominated_one(a, a <> t) == True{} : Bool}:
  match a:
    case P.Allow{}:
      {==}
    case P.Audit{}:
      {==}
    case P.Deny{}:
      {==}

law dom_refl:
  for +xs: +List<P.Decision>
  {P.dominated(xs, xs) == True{} : Bool}
def dom_refl(xs):
  match xs:
    case Nil{}:
      {==}
    case Con{+a, +t}:
      and_intro(P.dominated_one(a, a <> t), P.dominated(t, a <> t), one_self(a, t), dom_target_right(t, [a], t, dom_refl(t)))

law decisions_cat:
  for +xs: +List<P.Contrib>
  for +ys: +List<P.Contrib>
  {P.decisions(P.ccat(xs, ys)) == P.cat(P.decisions(xs), P.decisions(ys)) : +List<P.Decision>}
def decisions_cat(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{P.Contrib{+d, k}, +t}:
      %decisions_cat(t, ys) : {d <> P.decisions(P.ccat(t, ys)) == d <> _ : +List<P.Decision>}
      {==}

law Dom.refl:
  for +xs: +List<P.Contrib>
  {Dom(xs, xs) == True{} : Bool}
def Dom.refl(xs):
  dom_refl(P.decisions(xs))

law Dom.nil:
  for +ys: +List<P.Contrib>
  {Dom([], ys) == True{} : Bool}
def Dom.nil(ys):
  {==}

law Dom.single:
  for +d: P.Decision
  for +e: P.Decision
  for +k: P.Cause
  for +kk: P.Cause
  for h: {P.le(d, e) == True{} : Bool}
  {Dom([P.Contrib{d, k}], [P.Contrib{e, kk}]) == True{} : Bool}
def Dom.single(d, e, k, kk, h):
  %Equal.sym(Bool, P.le(d, e), True{}, h) : {Bool.and(Bool.or(_, False{}), True{}) == True{} : Bool}
  {==}

law Dom.append:
  for +a: +List<P.Contrib>
  for +aa: +List<P.Contrib>
  for +b: +List<P.Contrib>
  for +bb: +List<P.Contrib>
  for ha: {Dom(a, aa) == True{} : Bool}
  for hb: {Dom(b, bb) == True{} : Bool}
  {Dom(P.ccat(a, b), P.ccat(aa, bb)) == True{} : Bool}
def Dom.append(a, aa, b, bb, ha, hb):
  %Equal.sym(+List<P.Decision>, P.decisions(P.ccat(a, b)), P.cat(P.decisions(a), P.decisions(b)), decisions_cat(a, b)) : {P.dominated(_, P.decisions(P.ccat(aa, bb))) == True{} : Bool}
  %Equal.sym(+List<P.Decision>, P.decisions(P.ccat(aa, bb)), P.cat(P.decisions(aa), P.decisions(bb)), decisions_cat(aa, bb)) : {P.dominated(P.cat(P.decisions(a), P.decisions(b)), _) == True{} : Bool}
  Equal.trans(Bool, P.dominated(P.cat(P.decisions(a), P.decisions(b)), P.cat(P.decisions(aa), P.decisions(bb))), Bool.and(P.dominated(P.decisions(a), P.cat(P.decisions(aa), P.decisions(bb))), P.dominated(P.decisions(b), P.cat(P.decisions(aa), P.decisions(bb)))), True{}, dom_source_append(P.decisions(a), P.decisions(b), P.cat(P.decisions(aa), P.decisions(bb))), and_intro(P.dominated(P.decisions(a), P.cat(P.decisions(aa), P.decisions(bb))), P.dominated(P.decisions(b), P.cat(P.decisions(aa), P.decisions(bb))), dom_target_left(P.decisions(a), P.decisions(aa), P.decisions(bb), ha), dom_target_right(P.decisions(b), P.decisions(aa), P.decisions(bb), hb)))

law Dom.fold:
  for +xs: +List<P.Contrib>
  for +ys: +List<P.Contrib>
  for h: {Dom(xs, ys) == True{} : Bool}
  {P.le(P.fold(P.decisions(xs)), P.fold(P.decisions(ys))) == True{} : Bool}
def Dom.fold(xs, ys, h):
  P.mp(Dom(xs, ys), P.le(P.fold(P.decisions(xs)), P.fold(P.decisions(ys))), P.fold_le_of_dominated(P.decisions(xs), P.decisions(ys)), h)

# BEGIN OVERLAY PROOFS: helpers and the seven remaining Config theorems.
# The model, Env and all earlier declarations above are unchanged.

law protected_any_append:
  for +xs: +List<String>
  for +ys: +List<String>
  for +table: +List<P.ProtHit>
  for +value: String
  {P.protected_any(List.append(&2, String, xs, ys), table, value) == Bool.or(P.protected_any(xs, table, value), P.protected_any(ys, table, value)) : Bool}
def protected_any_append(xs, ys, table, value):
  match xs:
    case Nil{}:
      {==}
    case Con{+h, +t}:
      %Equal.sym(Bool, P.protected_any(List.append(&2, String, t, ys), table, value), Bool.or(P.protected_any(t, table, value), P.protected_any(ys, table, value)), protected_any_append(t, ys, table, value)) : {Bool.or(P.lookup_prot(table, h, value), _) == Bool.or(P.protected_any(h <> t, table, value), P.protected_any(ys, table, value)) : Bool}
      or_assoc(P.lookup_prot(table, h, value), P.protected_any(t, table, value), P.protected_any(ys, table, value))

law empty_append:
  for +xs: +List<String>
  for +ys: +List<String>
  {List.is_empty(&2, String, List.append(&2, String, xs, ys)) == Bool.and(List.is_empty(&2, String, xs), List.is_empty(&2, String, ys)) : Bool}
def empty_append(xs, ys):
  match xs:
    case Nil{}:
      {==}
    case Con{h, t}:
      {==}

def implies_refl(+b: Bool) -> {P.implies(b, b) == True{} : Bool}:
  match b:
    case False{}:
      {==}
    case True{}:
      {==}

def protected_guard_bits(+a: Bool, +b: Bool, +w: Bool) -> {P.implies(Bool.not(Bool.or(a, Bool.not(w))), Bool.not(Bool.or(Bool.and(a, b), Bool.not(w)))) == True{} : Bool}:
  match a w:
    case True{} _:
      {==}
    case False{} False{}:
      {==}
    case False{} True{}:
      {==}

def protected_guard_ge(+xs: +List<String>, +ys: +List<String>, +w: Bool) -> {P.implies(Bool.not(Bool.or(List.is_empty(&2, String, xs), Bool.not(w))), Bool.not(Bool.or(List.is_empty(&2, String, List.append(&2, String, xs, ys)), Bool.not(w)))) == True{} : Bool}:
  %Equal.sym(Bool, List.is_empty(&2, String, List.append(&2, String, xs, ys)), Bool.and(List.is_empty(&2, String, xs), List.is_empty(&2, String, ys)), empty_append(xs, ys)) : {P.implies(Bool.not(Bool.or(List.is_empty(&2, String, xs), Bool.not(w))), Bool.not(Bool.or(_, Bool.not(w)))) == True{} : Bool}
  protected_guard_bits(List.is_empty(&2, String, xs), List.is_empty(&2, String, ys), w)

def protected_hit_bits(+u: Bool, +a: Bool, +b: Bool) -> {P.implies(Bool.and(u, a), Bool.and(u, Bool.or(a, b))) == True{} : Bool}:
  match u a:
    case False{} _:
      {==}
    case True{} False{}:
      {==}
    case True{} True{}:
      {==}

def protected_hit_ge(+xs: +List<String>, +ys: +List<String>, +table: +List<P.ProtHit>, +value: String, +u: Bool) -> {P.implies(Bool.and(u, P.protected_any(xs, table, value)), Bool.and(u, P.protected_any(List.append(&2, String, xs, ys), table, value))) == True{} : Bool}:
  %Equal.sym(Bool, P.protected_any(List.append(&2, String, xs, ys), table, value), Bool.or(P.protected_any(xs, table, value), P.protected_any(ys, table, value)), protected_any_append(xs, ys, table, value)) : {P.implies(Bool.and(u, P.protected_any(xs, table, value)), Bool.and(u, _)) == True{} : Bool}
  protected_hit_bits(u, P.protected_any(xs, table, value), P.protected_any(ys, table, value))

def one_if_dom(+a: Bool, +b: Bool, +d: P.Decision, +dd: P.Decision, hab: {P.implies(a, b) == True{} : Bool}, hdd: {P.le(d, dd) == True{} : Bool}) -> {Dom(P.one_if(a, d, P.PolicyCause{}), P.one_if(b, dd, P.PolicyCause{})) == True{} : Bool}:
  match a b:
    case False{} b:
      Dom.nil(P.one_if(b, dd, P.PolicyCause{}))
    case True{} False{}:
      P.absurd({Dom([P.Contrib{d, P.PolicyCause{}}], []) == True{} : Bool}, hab)
    case True{} True{}:
      Dom.single(d, dd, P.PolicyCause{}, P.PolicyCause{}, hdd)

def unknown_dom(+i: Bool, +u: Bool, +um: P.Decision, +ui: P.Decision, +umm: P.Decision, +uii: P.Decision, hum: {P.le(um, umm) == True{} : Bool}, hui: {P.le(ui, uii) == True{} : Bool}) -> {Dom(Bool.pick(+List<P.Contrib>, i, [P.Contrib{ui, P.PolicyCause{}}], P.one_if(u, um, P.PolicyCause{})), Bool.pick(+List<P.Contrib>, i, [P.Contrib{uii, P.PolicyCause{}}], P.one_if(u, umm, P.PolicyCause{}))) == True{} : Bool}:
  match i:
    case False{}:
      one_if_dom(u, u, um, umm, implies_refl(u), hum)
    case True{}:
      Dom.single(ui, uii, P.PolicyCause{}, P.PolicyCause{}, hui)

def protected_dom(+blocked: Bool, +blocked2: Bool, +unresolved: Bool, +hit: Bool, +hit2: Bool, +d: P.Decision, +dd: P.Decision, hg: {P.implies(Bool.not(blocked), Bool.not(blocked2)) == True{} : Bool}, hh: {P.implies(hit, hit2) == True{} : Bool}, hd: {P.le(d, dd) == True{} : Bool}) -> {Dom(Bool.pick(+List<P.Contrib>, blocked, [], Bool.pick(+List<P.Contrib>, unresolved, [P.Contrib{d, P.PolicyCause{}}], P.one_if(hit, d, P.PolicyCause{}))), Bool.pick(+List<P.Contrib>, blocked2, [], Bool.pick(+List<P.Contrib>, unresolved, [P.Contrib{dd, P.PolicyCause{}}], P.one_if(hit2, dd, P.PolicyCause{})))) == True{} : Bool}:
  match blocked blocked2 unresolved:
    case True{} b z:
      Dom.nil(Bool.pick(+List<P.Contrib>, b, [], Bool.pick(+List<P.Contrib>, z, [P.Contrib{dd, P.PolicyCause{}}], P.one_if(hit2, dd, P.PolicyCause{}))))
    case False{} True{} z:
      P.absurd({Dom(Bool.pick(+List<P.Contrib>, z, [P.Contrib{d, P.PolicyCause{}}], P.one_if(hit, d, P.PolicyCause{})), []) == True{} : Bool}, hg)
    case False{} False{} True{}:
      Dom.single(d, dd, P.PolicyCause{}, P.PolicyCause{}, hd)
    case False{} False{} False{}:
      one_if_dom(hit, hit2, d, dd, hh, hd)

# evaluate.go:162-182; project.go:351-367.
law opContribs_dom:
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +o: P.Op
  {Dom(P.op_contribs(e, c, o), P.op_contribs(e, overlay(pr, c, p), o)) == True{} : Bool}
def opContribs_dom(e, pr, c, p, o):
  match e pr c p o:
    case P.Env{rs, inv, +table} Presence{pm, +pum, +pui, +ppu} P.Policy{cm, crs, +cum, +cui, +cpu, +cps} P.Policy{nm, nrs, +num, +nui, +npu, +nps} P.Op{key, +w, +u, +i, +repo, +less}:
      +um = raise(pum, cum, num)
      +ui = raise(pui, cui, nui)
      +pu = raise(ppu, cpu, npu)
      +ps = List.append(&2, String, cps, nps)
      +blocked = Bool.or(List.is_empty(&2, String, cps), Bool.not(w))
      +blocked2 = Bool.or(List.is_empty(&2, String, ps), Bool.not(w))
      +unresolved = Bool.and(P.repo_none(repo), Bool.not(less))
      +hit = Bool.and(u, P.protected_any(cps, table, P.repo_value(repo)))
      +hit2 = Bool.and(u, P.protected_any(ps, table, P.repo_value(repo)))
      +first = Bool.pick(+List<P.Contrib>, i, [P.Contrib{cui, P.PolicyCause{}}], P.one_if(u, cum, P.PolicyCause{}))
      +first2 = Bool.pick(+List<P.Contrib>, i, [P.Contrib{ui, P.PolicyCause{}}], P.one_if(u, um, P.PolicyCause{}))
      +second = Bool.pick(+List<P.Contrib>, blocked, [], Bool.pick(+List<P.Contrib>, unresolved, [P.Contrib{cpu, P.PolicyCause{}}], P.one_if(hit, cpu, P.PolicyCause{})))
      +second2 = Bool.pick(+List<P.Contrib>, blocked2, [], Bool.pick(+List<P.Contrib>, unresolved, [P.Contrib{pu, P.PolicyCause{}}], P.one_if(hit2, pu, P.PolicyCause{})))
      Dom.append(first, first2, second, second2, unknown_dom(i, u, cum, cui, um, ui, raise_ge(pum, cum, num), raise_ge(pui, cui, nui)), protected_dom(blocked, blocked2, unresolved, hit, hit2, cpu, pu, protected_guard_ge(cps, nps, w), protected_hit_ge(cps, nps, table, P.repo_value(repo), u), raise_ge(ppu, cpu, npu)))

# The old rules remain a prefix; rule contributions do not read Policy.
law rule_all_added:
  for +rs: +List<P.Rule>
  for +extra: +List<P.Rule>
  for +e: P.Env
  for +s: P.Subject
  {Dom(P.rule_all(rs, e, s), P.rule_all(List.append(&2, P.Rule, rs, extra), e, s)) == True{} : Bool}
def rule_all_added(rs, extra, e, s):
  match rs:
    case Nil{}:
      Dom.nil(P.rule_all(extra, e, s))
    case Con{+r, +t}:
      Dom.append(P.rule_contribs(e, s, r), P.rule_contribs(e, s, r), P.rule_all(t, e, s), P.rule_all(List.append(&2, P.Rule, t, extra), e, s), Dom.refl(P.rule_contribs(e, s, r)), rule_all_added(t, extra, e, s))

law op_all_dom:
  for +os: +List<P.Op>
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  {Dom(P.op_all(os, e, c), P.op_all(os, e, overlay(pr, c, p))) == True{} : Bool}
def op_all_dom(os, e, pr, c, p):
  match os:
    case Nil{}:
      {==}
    case Con{+o, +t}:
      Dom.append(P.op_contribs(e, c, o), P.op_contribs(e, overlay(pr, c, p), o), P.op_all(t, e, c), P.op_all(t, e, overlay(pr, c, p)), opContribs_dom(e, pr, c, p, o), op_all_dom(t, e, pr, c, p))

law contribs_dom:
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  {Dom(P.contribs(e, c, s), P.contribs(e, overlay(pr, c, p), s)) == True{} : Bool}
def contribs_dom(e, pr, c, p, s):
  match pr c p s:
    case Presence{+pm, +pum, +pui, +ppu} P.Policy{+cm, +crs, +cum, +cui, +cpu, +cps} P.Policy{+nm, +nrs, +num, +nui, +npu, +nps} P.Subject{+os, +no}:
      +pres = {Presence{pm, pum, pui, ppu} : Presence}
      +cur = {P.Policy{cm, crs, cum, cui, cpu, cps} : P.Policy}
      +new = {P.Policy{nm, nrs, num, nui, npu, nps} : P.Policy}
      +sub = {P.Subject{os, no} : P.Subject}
      +over = overlay(pres, cur, new)
      Dom.append(P.rule_all(crs, e, sub), P.rule_all(List.append(&2, P.Rule, crs, nrs), e, sub), P.op_all(os, e, cur), P.op_all(os, e, over), rule_all_added(crs, nrs, e, sub), op_all_dom(os, e, pres, cur, new))

law overlay_fold_ge:
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  {P.le(P.fold(P.decisions(P.contribs(e, c, s))), P.fold(P.decisions(P.contribs(e, overlay(pr, c, p), s)))) == True{} : Bool}
def overlay_fold_ge(e, pr, c, p, s):
  Dom.fold(P.contribs(e, c, s), P.contribs(e, overlay(pr, c, p), s), contribs_dom(e, pr, c, p, s))

def deny_up(+a: P.Decision, +b: P.Decision, hab: {P.le(a, b) == True{} : Bool}, ha: {P.is_deny(a) == True{} : Bool}) -> {P.is_deny(b) == True{} : Bool}:
  match a b:
    case P.Allow{} b:
      P.absurd({P.is_deny(b) == True{} : Bool}, ha)
    case P.Audit{} b:
      P.absurd({P.is_deny(b) == True{} : Bool}, ha)
    case P.Deny{} P.Allow{}:
      P.absurd({False{} == True{} : Bool}, hab)
    case P.Deny{} P.Audit{}:
      P.absurd({False{} == True{} : Bool}, hab)
    case P.Deny{} P.Deny{}:
      {==}

law overlay_wouldDeny:
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  for h: {P.ev_would_deny(P.evaluate(e, c, s)) == True{} : Bool}
  {P.ev_would_deny(P.evaluate(e, overlay(pr, c, p), s)) == True{} : Bool}
def overlay_wouldDeny(e, pr, c, p, s, h):
  deny_up(P.fold(P.decisions(P.contribs(e, c, s))), P.fold(P.decisions(P.contribs(e, overlay(pr, c, p), s))), overlay_fold_ge(e, pr, c, p, s), h)

law overlay_policy_mode_ge:
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  {Nat.is_le(P.mode_rank(P.policy_mode(c)), P.mode_rank(P.policy_mode(overlay(pr, c, p)))) == True{} : Bool}
def overlay_policy_mode_ge(pr, c, p):
  match pr c p:
    case Presence{pm, pum, pui, ppu} P.Policy{cm, crs, cum, cui, cpu, cps} P.Policy{nm, nrs, num, nui, npu, nps}:
      overlayMode_ge(pm, cm, nm)

# "overlays only tighten policy" (layering_fuzz_test.go:13).
law overlay_decision_ge:
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  {P.le(P.ev_decision(P.evaluate(e, c, s)), P.ev_decision(P.evaluate(e, overlay(pr, c, p), s))) == True{} : Bool}
def overlay_decision_ge(e, pr, c, p, s):
  +cm = P.policy_mode(c)
  +nm = P.policy_mode(overlay(pr, c, p))
  +cd = P.fold(P.decisions(P.contribs(e, c, s)))
  +nd = P.fold(P.decisions(P.contribs(e, overlay(pr, c, p), s)))
  P.mp(Bool.and(Nat.is_le(P.mode_rank(cm), P.mode_rank(nm)), P.le(cd, nd)), P.le(P.finalize(cm, cd), P.finalize(nm, nd)), P.finalize_mono(cm, nm, cd, nd), and_intro(Nat.is_le(P.mode_rank(cm), P.mode_rank(nm)), P.le(cd, nd), overlay_policy_mode_ge(pr, c, p), overlay_fold_ge(e, pr, c, p, s)))

law overlay_keeps_deny:
  for +e: P.Env
  for +pr: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +s: P.Subject
  for h: {P.is_deny(P.ev_decision(P.evaluate(e, c, s))) == True{} : Bool}
  {P.is_deny(P.ev_decision(P.evaluate(e, overlay(pr, c, p), s))) == True{} : Bool}
def overlay_keeps_deny(e, pr, c, p, s, h):
  deny_up(P.ev_decision(P.evaluate(e, c, s)), P.ev_decision(P.evaluate(e, overlay(pr, c, p), s)), overlay_decision_ge(e, pr, c, p, s), h)

def le_trans(+a: P.Decision, +b: P.Decision, +c: P.Decision, hab: {P.le(a, b) == True{} : Bool}, hbc: {P.le(b, c) == True{} : Bool}) -> {P.le(a, c) == True{} : Bool}:
  match a b c:
    case P.Allow{} _ _:
      {==}
    case P.Audit{} P.Allow{} c:
      P.absurd({P.le(P.Audit{}, c) == True{} : Bool}, hab)
    case P.Audit{} P.Audit{} c:
      hbc
    case P.Audit{} P.Deny{} P.Allow{}:
      P.absurd({False{} == True{} : Bool}, hbc)
    case P.Audit{} P.Deny{} P.Audit{}:
      {==}
    case P.Audit{} P.Deny{} P.Deny{}:
      {==}
    case P.Deny{} P.Allow{} c:
      P.absurd({P.le(P.Deny{}, c) == True{} : Bool}, hab)
    case P.Deny{} P.Audit{} c:
      P.absurd({P.le(P.Deny{}, c) == True{} : Bool}, hab)
    case P.Deny{} P.Deny{} c:
      hbc

# user -> .ghok.yaml -> .ghok.local.yaml.
law overlay_chain_ge:
  for +e: P.Env
  for +pr: Presence
  for +pr2: Presence
  for +c: P.Policy
  for +p: P.Policy
  for +local: P.Policy
  for +s: P.Subject
  {P.le(P.ev_decision(P.evaluate(e, c, s)), P.ev_decision(P.evaluate(e, overlay(pr2, overlay(pr, c, p), local), s))) == True{} : Bool}
def overlay_chain_ge(e, pr, pr2, c, p, local, s):
  le_trans(P.ev_decision(P.evaluate(e, c, s)), P.ev_decision(P.evaluate(e, overlay(pr, c, p), s)), P.ev_decision(P.evaluate(e, overlay(pr2, overlay(pr, c, p), local), s)), overlay_decision_ge(e, pr, c, p, s), overlay_decision_ge(e, pr2, overlay(pr, c, p), local, s))
# END OVERLAY PROOFS.

