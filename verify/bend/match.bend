# Port of Ghok/Match.lean; match.go:210,242; evaluate.go:145.
# Bool equalities replace Prop exists/or/iff. Base Maybe<Bool> preserves exactly
# none=no match, some false=definite, some true=maybe.
# QUOTIENT MODEL: arbitrary glob/other closures are replaced by per-matcher
# cached bits. A resolved target stores any(glob patterns value); None is
# unresolved. Mask stores patterns.isEmpty, other(base), RepositoryLess.
# Refines pairs share a Mask and preserve each already-resolved cache.
# RepositoryLess stays fixed: unresolved repositories are not RepositoryLess
# (match.go:143). Theorems do not need that additional input invariant. A list
# of such pairs represents unless matchers before/after resolution. This proves
# a stronger, independent-cache result; every Lean refinement induces these
# pairs. It does not implement globs or re-prove cache construction from strings.
import Base
import ./policy.bend as P

type Mask is Data:
  Mask{other: Bool, repo_less: Bool, repos_empty: Bool, hosts_empty: Bool}
type Refinement is Data:
  Refines{mask: Mask, repo: +Maybe<Bool>, repo_after: +Maybe<Bool>, host: +Maybe<Bool>, host_after: +Maybe<Bool>}

def bool_eq(a: Bool, b: Bool) -> Bool:
  Bool.not(Bool.xor(a, b))

def target_refines(a: +Maybe<Bool>, b: +Maybe<Bool>) -> Bool:
  match a b:
    case None{} _:
      True{}
    case Some{x} None{}:
      False{}
    case Some{x} Some{y}:
      bool_eq(x, y)

# One target criterion (match.go target matching): None is unresolved here.
def crit(empty: Bool, v: +Maybe<Bool>) -> +Maybe<Bool>:
  match empty v:
    case True{} _:
      Some{False{}}
    case False{} None{}:
      Some{True{}}
    case False{} Some{hit}:
      Bool.pick(+Maybe<Bool>, hit, Some{False{}}, None{})

def definite(v: +Maybe<Bool>) -> Bool:
  match v:
    case Some{False{}}:
      True{}
    case _:
      False{}

def present(v: +Maybe<Bool>) -> Bool:
  match v:
    case None{}:
      False{}
    case Some{b}:
      True{}

# Exact Boolean encoding of exists b', before=some b' /\ (b=true -> b'=true).
def match_refines(a: +Maybe<Bool>, b: +Maybe<Bool>) -> Bool:
  match a b:
    case _ None{}:
      True{}
    case None{} Some{b}:
      False{}
    case Some{x} Some{y}:
      P.implies(y, x)

# match.go:210. guard includes other(base) and the RepositoryLess veto.
def guard(m: Mask) -> Bool:
  Mask{o, less, re, he} = m
  Bool.and(o, Bool.not(Bool.and(Bool.not(re), less)))

def combine(g: Bool, a: +Maybe<Bool>, b: +Maybe<Bool>) -> +Maybe<Bool>:
  match g a b:
    case False{} _ _:
      None{}
    case True{} Some{x} Some{y}:
      Some{Bool.or(x, y)}
    case True{} _ _:
      None{}

def op_match(m: Mask, r: +Maybe<Bool>, h: +Maybe<Bool>) -> +Maybe<Bool>:
  Mask{+o, +less, +re, +he} = m
  combine(guard(Mask{o, less, re, he}), crit(re, r), crit(he, h))

def before(r: Refinement) -> +Maybe<Bool>:
  Refines{m, repo, after, host, ha} = r
  op_match(m, repo, host)

def after(r: Refinement) -> +Maybe<Bool>:
  Refines{m, repo, ra, host, ha} = r
  op_match(m, ra, ha)

def valid(r: Refinement) -> Bool:
  Refines{m, repo, ra, host, ha} = r
  Bool.and(target_refines(repo, ra), target_refines(host, ha))

# match.go:239,242: Maybe-matches never exclude.
def excluded_before(us: +List<Refinement>) -> Bool:
  match us:
    case Nil{}:
      False{}
    case Con{r, t}:
      Bool.or(definite(before(r)), excluded_before(t))

def excluded_after(us: +List<Refinement>) -> Bool:
  match us:
    case Nil{}:
      False{}
    case Con{r, t}:
      Bool.or(definite(after(r)), excluded_after(t))

def valid_all(us: +List<Refinement>) -> Bool:
  match us:
    case Nil{}:
      True{}
    case Con{r, t}:
      Bool.and(valid(r), valid_all(t))

# evaluate.go:145: onUnresolved + unless.
def fires_value(v: +Maybe<Bool>, excluded: Bool, ignore: Bool) -> Bool:
  match v:
    case None{}:
      False{}
    case Some{maybe}:
      Bool.and(Bool.not(Bool.and(maybe, ignore)), Bool.not(excluded))

def fires_before(r: Refinement, us: +List<Refinement>, ignore: Bool) -> Bool:
  fires_value(before(r), excluded_before(us), ignore)

def fires_after(r: Refinement, us: +List<Refinement>, ignore: Bool) -> Bool:
  fires_value(after(r), excluded_after(us), ignore)

law crit_refine:
  for +empty: Bool
  for +v: +Maybe<Bool>
  for +w: +Maybe<Bool>
  for h: {target_refines(v, w) == True{} : Bool}
  {match_refines(crit(empty, v), crit(empty, w)) == True{} : Bool}
def crit_refine(empty, v, w, h):
  match empty v w:
    case False{} None{} None{}:
      {==}
    case False{} None{} Some{False{}}:
      {==}
    case False{} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} None{}:
      P.absurd({match_refines(crit(False{}, Some{False{}}), crit(False{}, None{})) == True{} : Bool}, h)
    case False{} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}}:
      P.absurd({match_refines(crit(False{}, Some{False{}}), crit(False{}, Some{True{}})) == True{} : Bool}, h)
    case False{} Some{True{}} None{}:
      P.absurd({match_refines(crit(False{}, Some{True{}}), crit(False{}, None{})) == True{} : Bool}, h)
    case False{} Some{True{}} Some{False{}}:
      P.absurd({match_refines(crit(False{}, Some{True{}}), crit(False{}, Some{False{}})) == True{} : Bool}, h)
    case False{} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} None{}:
      {==}
    case True{} None{} Some{False{}}:
      {==}
    case True{} None{} Some{True{}}:
      {==}
    case True{} Some{False{}} None{}:
      P.absurd({match_refines(crit(True{}, Some{False{}}), crit(True{}, None{})) == True{} : Bool}, h)
    case True{} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{True{}}:
      P.absurd({match_refines(crit(True{}, Some{False{}}), crit(True{}, Some{True{}})) == True{} : Bool}, h)
    case True{} Some{True{}} None{}:
      P.absurd({match_refines(crit(True{}, Some{True{}}), crit(True{}, None{})) == True{} : Bool}, h)
    case True{} Some{True{}} Some{False{}}:
      P.absurd({match_refines(crit(True{}, Some{True{}}), crit(True{}, Some{False{}})) == True{} : Bool}, h)
    case True{} Some{True{}} Some{True{}}:
      {==}

law crit_definite:
  for +empty: Bool
  for +v: +Maybe<Bool>
  for +w: +Maybe<Bool>
  for h: {target_refines(v, w) == True{} : Bool}
  {P.implies(definite(crit(empty, v)), definite(crit(empty, w))) == True{} : Bool}
def crit_definite(empty, v, w, h):
  match empty v w:
    case False{} None{} None{}:
      {==}
    case False{} None{} Some{False{}}:
      {==}
    case False{} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} None{}:
      P.absurd({P.implies(definite(crit(False{}, Some{False{}})), definite(crit(False{}, None{}))) == True{} : Bool}, h)
    case False{} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}}:
      P.absurd({P.implies(definite(crit(False{}, Some{False{}})), definite(crit(False{}, Some{True{}}))) == True{} : Bool}, h)
    case False{} Some{True{}} None{}:
      P.absurd({P.implies(definite(crit(False{}, Some{True{}})), definite(crit(False{}, None{}))) == True{} : Bool}, h)
    case False{} Some{True{}} Some{False{}}:
      P.absurd({P.implies(definite(crit(False{}, Some{True{}})), definite(crit(False{}, Some{False{}}))) == True{} : Bool}, h)
    case False{} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} None{}:
      {==}
    case True{} None{} Some{False{}}:
      {==}
    case True{} None{} Some{True{}}:
      {==}
    case True{} Some{False{}} None{}:
      P.absurd({P.implies(definite(crit(True{}, Some{False{}})), definite(crit(True{}, None{}))) == True{} : Bool}, h)
    case True{} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{True{}}:
      P.absurd({P.implies(definite(crit(True{}, Some{False{}})), definite(crit(True{}, Some{True{}}))) == True{} : Bool}, h)
    case True{} Some{True{}} None{}:
      P.absurd({P.implies(definite(crit(True{}, Some{True{}})), definite(crit(True{}, None{}))) == True{} : Bool}, h)
    case True{} Some{True{}} Some{False{}}:
      P.absurd({P.implies(definite(crit(True{}, Some{True{}})), definite(crit(True{}, Some{False{}}))) == True{} : Bool}, h)
    case True{} Some{True{}} Some{True{}}:
      {==}

def combine_refine(+g: Bool, +a: +Maybe<Bool>, +aa: +Maybe<Bool>, +b: +Maybe<Bool>, +bb: +Maybe<Bool>, ha: {match_refines(a, aa) == True{} : Bool}, hb: {match_refines(b, bb) == True{} : Bool}) -> {match_refines(combine(g, a, b), combine(g, aa, bb)) == True{} : Bool}:
  match g a aa b bb:
    case False{} None{} None{} None{} None{}:
      {==}
    case False{} None{} None{} None{} Some{False{}}:
      {==}
    case False{} None{} None{} None{} Some{True{}}:
      {==}
    case False{} None{} None{} Some{False{}} None{}:
      {==}
    case False{} None{} None{} Some{False{}} Some{False{}}:
      {==}
    case False{} None{} None{} Some{False{}} Some{True{}}:
      {==}
    case False{} None{} None{} Some{True{}} None{}:
      {==}
    case False{} None{} None{} Some{True{}} Some{False{}}:
      {==}
    case False{} None{} None{} Some{True{}} Some{True{}}:
      {==}
    case False{} None{} Some{False{}} None{} None{}:
      {==}
    case False{} None{} Some{False{}} None{} Some{False{}}:
      {==}
    case False{} None{} Some{False{}} None{} Some{True{}}:
      {==}
    case False{} None{} Some{False{}} Some{False{}} None{}:
      {==}
    case False{} None{} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case False{} None{} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case False{} None{} Some{False{}} Some{True{}} None{}:
      {==}
    case False{} None{} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case False{} None{} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case False{} None{} Some{True{}} None{} None{}:
      {==}
    case False{} None{} Some{True{}} None{} Some{False{}}:
      {==}
    case False{} None{} Some{True{}} None{} Some{True{}}:
      {==}
    case False{} None{} Some{True{}} Some{False{}} None{}:
      {==}
    case False{} None{} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case False{} None{} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case False{} None{} Some{True{}} Some{True{}} None{}:
      {==}
    case False{} None{} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case False{} None{} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{False{}} None{} None{} None{}:
      {==}
    case False{} Some{False{}} None{} None{} Some{False{}}:
      {==}
    case False{} Some{False{}} None{} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} None{} Some{False{}} None{}:
      {==}
    case False{} Some{False{}} None{} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} None{} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{False{}} None{} Some{True{}} None{}:
      {==}
    case False{} Some{False{}} None{} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{False{}} None{} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{False{}} None{} None{}:
      {==}
    case False{} Some{False{}} Some{False{}} None{} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{False{}} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{False{}} None{}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{True{}} None{}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{True{}} None{} None{}:
      {==}
    case False{} Some{False{}} Some{True{}} None{} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{False{}} None{}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{True{}} None{}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{True{}} None{} None{} None{}:
      {==}
    case False{} Some{True{}} None{} None{} Some{False{}}:
      {==}
    case False{} Some{True{}} None{} None{} Some{True{}}:
      {==}
    case False{} Some{True{}} None{} Some{False{}} None{}:
      {==}
    case False{} Some{True{}} None{} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{True{}} None{} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{True{}} None{} Some{True{}} None{}:
      {==}
    case False{} Some{True{}} None{} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{True{}} None{} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{False{}} None{} None{}:
      {==}
    case False{} Some{True{}} Some{False{}} None{} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{False{}} None{} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{False{}} None{}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{True{}} None{}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{True{}} None{} None{}:
      {==}
    case False{} Some{True{}} Some{True{}} None{} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{True{}} None{} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{False{}} None{}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{True{}} None{}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} None{} None{} None{}:
      {==}
    case True{} None{} None{} None{} Some{False{}}:
      {==}
    case True{} None{} None{} None{} Some{True{}}:
      {==}
    case True{} None{} None{} Some{False{}} None{}:
      {==}
    case True{} None{} None{} Some{False{}} Some{False{}}:
      {==}
    case True{} None{} None{} Some{False{}} Some{True{}}:
      {==}
    case True{} None{} None{} Some{True{}} None{}:
      {==}
    case True{} None{} None{} Some{True{}} Some{False{}}:
      {==}
    case True{} None{} None{} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} Some{False{}} None{} None{}:
      {==}
    case True{} None{} Some{False{}} None{} Some{False{}}:
      P.absurd({match_refines(combine(True{}, None{}, None{}), combine(True{}, Some{False{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} None{} Some{False{}} None{} Some{True{}}:
      P.absurd({match_refines(combine(True{}, None{}, None{}), combine(True{}, Some{False{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} None{} Some{False{}} Some{False{}} None{}:
      {==}
    case True{} None{} Some{False{}} Some{False{}} Some{False{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{False{}}), combine(True{}, Some{False{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} None{} Some{False{}} Some{False{}} Some{True{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{False{}}), combine(True{}, Some{False{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} None{} Some{False{}} Some{True{}} None{}:
      {==}
    case True{} None{} Some{False{}} Some{True{}} Some{False{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{True{}}), combine(True{}, Some{False{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} None{} Some{False{}} Some{True{}} Some{True{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{True{}}), combine(True{}, Some{False{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} None{} Some{True{}} None{} None{}:
      {==}
    case True{} None{} Some{True{}} None{} Some{False{}}:
      P.absurd({match_refines(combine(True{}, None{}, None{}), combine(True{}, Some{True{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} None{} Some{True{}} None{} Some{True{}}:
      P.absurd({match_refines(combine(True{}, None{}, None{}), combine(True{}, Some{True{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} None{} Some{True{}} Some{False{}} None{}:
      {==}
    case True{} None{} Some{True{}} Some{False{}} Some{False{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{False{}}), combine(True{}, Some{True{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} None{} Some{True{}} Some{False{}} Some{True{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{False{}}), combine(True{}, Some{True{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} None{} Some{True{}} Some{True{}} None{}:
      {==}
    case True{} None{} Some{True{}} Some{True{}} Some{False{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{True{}}), combine(True{}, Some{True{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} None{} Some{True{}} Some{True{}} Some{True{}}:
      P.absurd({match_refines(combine(True{}, None{}, Some{True{}}), combine(True{}, Some{True{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} Some{False{}} None{} None{} None{}:
      {==}
    case True{} Some{False{}} None{} None{} Some{False{}}:
      {==}
    case True{} Some{False{}} None{} None{} Some{True{}}:
      {==}
    case True{} Some{False{}} None{} Some{False{}} None{}:
      {==}
    case True{} Some{False{}} None{} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{False{}} None{} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{False{}} None{} Some{True{}} None{}:
      {==}
    case True{} Some{False{}} None{} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{False{}} None{} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{False{}} Some{False{}} None{} None{}:
      {==}
    case True{} Some{False{}} Some{False{}} None{} Some{False{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, None{}), combine(True{}, Some{False{}}, Some{False{}})) == True{} : Bool}, hb)
    case True{} Some{False{}} Some{False{}} None{} Some{True{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, None{}), combine(True{}, Some{False{}}, Some{True{}})) == True{} : Bool}, hb)
    case True{} Some{False{}} Some{False{}} Some{False{}} None{}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{False{}} Some{True{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, Some{False{}}), combine(True{}, Some{False{}}, Some{True{}})) == True{} : Bool}, hb)
    case True{} Some{False{}} Some{False{}} Some{True{}} None{}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{False{}} Some{True{}} None{} None{}:
      {==}
    case True{} Some{False{}} Some{True{}} None{} Some{False{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, None{}), combine(True{}, Some{True{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} None{} Some{True{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, None{}), combine(True{}, Some{True{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} Some{False{}} None{}:
      {==}
    case True{} Some{False{}} Some{True{}} Some{False{}} Some{False{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, Some{False{}}), combine(True{}, Some{True{}}, Some{False{}})) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} Some{False{}} Some{True{}}:
      P.absurd({match_refines(combine(True{}, Some{False{}}, Some{False{}}), combine(True{}, Some{True{}}, Some{True{}})) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} Some{True{}} None{}:
      {==}
    case True{} Some{False{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{True{}} None{} None{} None{}:
      {==}
    case True{} Some{True{}} None{} None{} Some{False{}}:
      {==}
    case True{} Some{True{}} None{} None{} Some{True{}}:
      {==}
    case True{} Some{True{}} None{} Some{False{}} None{}:
      {==}
    case True{} Some{True{}} None{} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{True{}} None{} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{True{}} None{} Some{True{}} None{}:
      {==}
    case True{} Some{True{}} None{} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{True{}} None{} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{False{}} None{} None{}:
      {==}
    case True{} Some{True{}} Some{False{}} None{} Some{False{}}:
      P.absurd({match_refines(combine(True{}, Some{True{}}, None{}), combine(True{}, Some{False{}}, Some{False{}})) == True{} : Bool}, hb)
    case True{} Some{True{}} Some{False{}} None{} Some{True{}}:
      P.absurd({match_refines(combine(True{}, Some{True{}}, None{}), combine(True{}, Some{False{}}, Some{True{}})) == True{} : Bool}, hb)
    case True{} Some{True{}} Some{False{}} Some{False{}} None{}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{True{}} None{}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{True{}} None{} None{}:
      {==}
    case True{} Some{True{}} Some{True{}} None{} Some{False{}}:
      P.absurd({match_refines(combine(True{}, Some{True{}}, None{}), combine(True{}, Some{True{}}, Some{False{}})) == True{} : Bool}, hb)
    case True{} Some{True{}} Some{True{}} None{} Some{True{}}:
      P.absurd({match_refines(combine(True{}, Some{True{}}, None{}), combine(True{}, Some{True{}}, Some{True{}})) == True{} : Bool}, hb)
    case True{} Some{True{}} Some{True{}} Some{False{}} None{}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{True{}} None{}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}

def combine_definite(+g: Bool, +a: +Maybe<Bool>, +aa: +Maybe<Bool>, +b: +Maybe<Bool>, +bb: +Maybe<Bool>, ha: {P.implies(definite(a), definite(aa)) == True{} : Bool}, hb: {P.implies(definite(b), definite(bb)) == True{} : Bool}) -> {P.implies(definite(combine(g, a, b)), definite(combine(g, aa, bb))) == True{} : Bool}:
  match g a aa b bb:
    case False{} None{} None{} None{} None{}:
      {==}
    case False{} None{} None{} None{} Some{False{}}:
      {==}
    case False{} None{} None{} None{} Some{True{}}:
      {==}
    case False{} None{} None{} Some{False{}} None{}:
      {==}
    case False{} None{} None{} Some{False{}} Some{False{}}:
      {==}
    case False{} None{} None{} Some{False{}} Some{True{}}:
      {==}
    case False{} None{} None{} Some{True{}} None{}:
      {==}
    case False{} None{} None{} Some{True{}} Some{False{}}:
      {==}
    case False{} None{} None{} Some{True{}} Some{True{}}:
      {==}
    case False{} None{} Some{False{}} None{} None{}:
      {==}
    case False{} None{} Some{False{}} None{} Some{False{}}:
      {==}
    case False{} None{} Some{False{}} None{} Some{True{}}:
      {==}
    case False{} None{} Some{False{}} Some{False{}} None{}:
      {==}
    case False{} None{} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case False{} None{} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case False{} None{} Some{False{}} Some{True{}} None{}:
      {==}
    case False{} None{} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case False{} None{} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case False{} None{} Some{True{}} None{} None{}:
      {==}
    case False{} None{} Some{True{}} None{} Some{False{}}:
      {==}
    case False{} None{} Some{True{}} None{} Some{True{}}:
      {==}
    case False{} None{} Some{True{}} Some{False{}} None{}:
      {==}
    case False{} None{} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case False{} None{} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case False{} None{} Some{True{}} Some{True{}} None{}:
      {==}
    case False{} None{} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case False{} None{} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{False{}} None{} None{} None{}:
      {==}
    case False{} Some{False{}} None{} None{} Some{False{}}:
      {==}
    case False{} Some{False{}} None{} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} None{} Some{False{}} None{}:
      {==}
    case False{} Some{False{}} None{} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} None{} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{False{}} None{} Some{True{}} None{}:
      {==}
    case False{} Some{False{}} None{} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{False{}} None{} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{False{}} None{} None{}:
      {==}
    case False{} Some{False{}} Some{False{}} None{} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{False{}} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{False{}} None{}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{True{}} None{}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{True{}} None{} None{}:
      {==}
    case False{} Some{False{}} Some{True{}} None{} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}} None{} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{False{}} None{}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{True{}} None{}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{False{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{True{}} None{} None{} None{}:
      {==}
    case False{} Some{True{}} None{} None{} Some{False{}}:
      {==}
    case False{} Some{True{}} None{} None{} Some{True{}}:
      {==}
    case False{} Some{True{}} None{} Some{False{}} None{}:
      {==}
    case False{} Some{True{}} None{} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{True{}} None{} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{True{}} None{} Some{True{}} None{}:
      {==}
    case False{} Some{True{}} None{} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{True{}} None{} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{False{}} None{} None{}:
      {==}
    case False{} Some{True{}} Some{False{}} None{} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{False{}} None{} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{False{}} None{}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{True{}} None{}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{True{}} None{} None{}:
      {==}
    case False{} Some{True{}} Some{True{}} None{} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{True{}} None{} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{False{}} None{}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{True{}} None{}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case False{} Some{True{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} None{} None{} None{}:
      {==}
    case True{} None{} None{} None{} Some{False{}}:
      {==}
    case True{} None{} None{} None{} Some{True{}}:
      {==}
    case True{} None{} None{} Some{False{}} None{}:
      {==}
    case True{} None{} None{} Some{False{}} Some{False{}}:
      {==}
    case True{} None{} None{} Some{False{}} Some{True{}}:
      {==}
    case True{} None{} None{} Some{True{}} None{}:
      {==}
    case True{} None{} None{} Some{True{}} Some{False{}}:
      {==}
    case True{} None{} None{} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} Some{False{}} None{} None{}:
      {==}
    case True{} None{} Some{False{}} None{} Some{False{}}:
      {==}
    case True{} None{} Some{False{}} None{} Some{True{}}:
      {==}
    case True{} None{} Some{False{}} Some{False{}} None{}:
      {==}
    case True{} None{} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case True{} None{} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case True{} None{} Some{False{}} Some{True{}} None{}:
      {==}
    case True{} None{} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case True{} None{} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case True{} None{} Some{True{}} None{} None{}:
      {==}
    case True{} None{} Some{True{}} None{} Some{False{}}:
      {==}
    case True{} None{} Some{True{}} None{} Some{True{}}:
      {==}
    case True{} None{} Some{True{}} Some{False{}} None{}:
      {==}
    case True{} None{} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case True{} None{} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case True{} None{} Some{True{}} Some{True{}} None{}:
      {==}
    case True{} None{} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case True{} None{} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{False{}} None{} None{} None{}:
      {==}
    case True{} Some{False{}} None{} None{} Some{False{}}:
      {==}
    case True{} Some{False{}} None{} None{} Some{True{}}:
      {==}
    case True{} Some{False{}} None{} Some{False{}} None{}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, None{}, None{}))) == True{} : Bool}, ha)
    case True{} Some{False{}} None{} Some{False{}} Some{False{}}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, None{}, Some{False{}}))) == True{} : Bool}, ha)
    case True{} Some{False{}} None{} Some{False{}} Some{True{}}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, None{}, Some{True{}}))) == True{} : Bool}, ha)
    case True{} Some{False{}} None{} Some{True{}} None{}:
      {==}
    case True{} Some{False{}} None{} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{False{}} None{} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{False{}} Some{False{}} None{} None{}:
      {==}
    case True{} Some{False{}} Some{False{}} None{} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{False{}} None{} Some{True{}}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{False{}} None{}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, Some{False{}}, None{}))) == True{} : Bool}, hb)
    case True{} Some{False{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{False{}} Some{True{}}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, Some{False{}}, Some{True{}}))) == True{} : Bool}, hb)
    case True{} Some{False{}} Some{False{}} Some{True{}} None{}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{False{}} Some{True{}} None{} None{}:
      {==}
    case True{} Some{False{}} Some{True{}} None{} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{True{}} None{} Some{True{}}:
      {==}
    case True{} Some{False{}} Some{True{}} Some{False{}} None{}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, Some{True{}}, None{}))) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} Some{False{}} Some{False{}}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, Some{True{}}, Some{False{}}))) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} Some{False{}} Some{True{}}:
      P.absurd({P.implies(definite(combine(True{}, Some{False{}}, Some{False{}})), definite(combine(True{}, Some{True{}}, Some{True{}}))) == True{} : Bool}, ha)
    case True{} Some{False{}} Some{True{}} Some{True{}} None{}:
      {==}
    case True{} Some{False{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{False{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{True{}} None{} None{} None{}:
      {==}
    case True{} Some{True{}} None{} None{} Some{False{}}:
      {==}
    case True{} Some{True{}} None{} None{} Some{True{}}:
      {==}
    case True{} Some{True{}} None{} Some{False{}} None{}:
      {==}
    case True{} Some{True{}} None{} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{True{}} None{} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{True{}} None{} Some{True{}} None{}:
      {==}
    case True{} Some{True{}} None{} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{True{}} None{} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{False{}} None{} None{}:
      {==}
    case True{} Some{True{}} Some{False{}} None{} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{False{}} None{} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{False{}} None{}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{True{}} None{}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{False{}} Some{True{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{True{}} None{} None{}:
      {==}
    case True{} Some{True{}} Some{True{}} None{} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{True{}} None{} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{False{}} None{}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{False{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{False{}} Some{True{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{True{}} None{}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{True{}} Some{False{}}:
      {==}
    case True{} Some{True{}} Some{True{}} Some{True{}} Some{True{}}:
      {==}

def and_left(+a: Bool, +b: Bool, h: {Bool.and(a, b) == True{} : Bool}) -> {a == True{} : Bool}:
  match a:
    case False{}:
      P.absurd({False{} == True{} : Bool}, h)
    case True{}:
      {==}

def and_right(+a: Bool, +b: Bool, h: {Bool.and(a, b) == True{} : Bool}) -> {b == True{} : Bool}:
  match a:
    case False{}:
      P.absurd({b == True{} : Bool}, h)
    case True{}:
      h

law opMatch_refine:
  for +r: Refinement
  for +hv: {valid(r) == True{} : Bool}
  {match_refines(before(r), after(r)) == True{} : Bool}
def opMatch_refine(r, hv):
  match r:
    case Refines{Mask{+o, +less, +re, +he}, +repo, +ra, +host, +ha}:
      combine_refine(guard(Mask{o, less, re, he}), crit(re, repo), crit(re, ra), crit(he, host), crit(he, ha), crit_refine(re, repo, ra, and_left(target_refines(repo, ra), target_refines(host, ha), hv)), crit_refine(he, host, ha, and_right(target_refines(repo, ra), target_refines(host, ha), hv)))

law opMatch_definite:
  for +r: Refinement
  for +hv: {valid(r) == True{} : Bool}
  {P.implies(definite(before(r)), definite(after(r))) == True{} : Bool}
def opMatch_definite(r, hv):
  match r:
    case Refines{Mask{+o, +less, +re, +he}, +repo, +ra, +host, +ha}:
      combine_definite(guard(Mask{o, less, re, he}), crit(re, repo), crit(re, ra), crit(he, host), crit(he, ha), crit_definite(re, repo, ra, and_left(target_refines(repo, ra), target_refines(host, ha), hv)), crit_definite(he, host, ha, and_right(target_refines(repo, ra), target_refines(host, ha), hv)))

def or_mono(+a: Bool, +aa: Bool, +b: Bool, +bb: Bool, ha: {P.implies(a, aa) == True{} : Bool}, hb: {P.implies(b, bb) == True{} : Bool}) -> {P.implies(Bool.or(a, b), Bool.or(aa, bb)) == True{} : Bool}:
  match a aa b bb:
    case False{} False{} False{} False{}:
      {==}
    case False{} False{} False{} True{}:
      {==}
    case False{} False{} True{} False{}:
      P.absurd({P.implies(Bool.or(False{}, True{}), Bool.or(False{}, False{})) == True{} : Bool}, hb)
    case False{} False{} True{} True{}:
      {==}
    case False{} True{} False{} False{}:
      {==}
    case False{} True{} False{} True{}:
      {==}
    case False{} True{} True{} False{}:
      {==}
    case False{} True{} True{} True{}:
      {==}
    case True{} False{} False{} False{}:
      P.absurd({P.implies(Bool.or(True{}, False{}), Bool.or(False{}, False{})) == True{} : Bool}, ha)
    case True{} False{} False{} True{}:
      {==}
    case True{} False{} True{} False{}:
      P.absurd({P.implies(Bool.or(True{}, True{}), Bool.or(False{}, False{})) == True{} : Bool}, ha)
    case True{} False{} True{} True{}:
      {==}
    case True{} True{} False{} False{}:
      {==}
    case True{} True{} False{} True{}:
      {==}
    case True{} True{} True{} False{}:
      {==}
    case True{} True{} True{} True{}:
      {==}

law excluded_refine:
  for +us: +List<Refinement>
  for +hv: {valid_all(us) == True{} : Bool}
  {P.implies(excluded_before(us), excluded_after(us)) == True{} : Bool}
def excluded_refine(us, hv):
  match us:
    case Nil{}:
      {==}
    case Con{+r, +t}:
      or_mono(definite(before(r)), definite(after(r)), excluded_before(t), excluded_after(t), opMatch_definite(r, and_left(valid(r), valid_all(t), hv)), excluded_refine(t, and_right(valid(r), valid_all(t), hv)))

def fire_conservative(+a: +Maybe<Bool>, +aa: +Maybe<Bool>, +x: Bool, +xx: Bool, hr: {match_refines(a, aa) == True{} : Bool}, hx: {P.implies(x, xx) == True{} : Bool}) -> {P.implies(fires_value(aa, xx, False{}), fires_value(a, x, False{})) == True{} : Bool}:
  match a aa x xx:
    case None{} None{} False{} False{}:
      {==}
    case None{} None{} False{} True{}:
      {==}
    case None{} None{} True{} False{}:
      {==}
    case None{} None{} True{} True{}:
      {==}
    case None{} Some{False{}} False{} False{}:
      P.absurd({P.implies(fires_value(Some{False{}}, False{}, False{}), fires_value(None{}, False{}, False{})) == True{} : Bool}, hr)
    case None{} Some{False{}} False{} True{}:
      {==}
    case None{} Some{False{}} True{} False{}:
      P.absurd({P.implies(fires_value(Some{False{}}, False{}, False{}), fires_value(None{}, True{}, False{})) == True{} : Bool}, hr)
    case None{} Some{False{}} True{} True{}:
      {==}
    case None{} Some{True{}} False{} False{}:
      P.absurd({P.implies(fires_value(Some{True{}}, False{}, False{}), fires_value(None{}, False{}, False{})) == True{} : Bool}, hr)
    case None{} Some{True{}} False{} True{}:
      {==}
    case None{} Some{True{}} True{} False{}:
      P.absurd({P.implies(fires_value(Some{True{}}, False{}, False{}), fires_value(None{}, True{}, False{})) == True{} : Bool}, hr)
    case None{} Some{True{}} True{} True{}:
      {==}
    case Some{False{}} None{} False{} False{}:
      {==}
    case Some{False{}} None{} False{} True{}:
      {==}
    case Some{False{}} None{} True{} False{}:
      {==}
    case Some{False{}} None{} True{} True{}:
      {==}
    case Some{False{}} Some{False{}} False{} False{}:
      {==}
    case Some{False{}} Some{False{}} False{} True{}:
      {==}
    case Some{False{}} Some{False{}} True{} False{}:
      P.absurd({P.implies(fires_value(Some{False{}}, False{}, False{}), fires_value(Some{False{}}, True{}, False{})) == True{} : Bool}, hx)
    case Some{False{}} Some{False{}} True{} True{}:
      {==}
    case Some{False{}} Some{True{}} False{} False{}:
      {==}
    case Some{False{}} Some{True{}} False{} True{}:
      {==}
    case Some{False{}} Some{True{}} True{} False{}:
      P.absurd({P.implies(fires_value(Some{True{}}, False{}, False{}), fires_value(Some{False{}}, True{}, False{})) == True{} : Bool}, hr)
    case Some{False{}} Some{True{}} True{} True{}:
      {==}
    case Some{True{}} None{} False{} False{}:
      {==}
    case Some{True{}} None{} False{} True{}:
      {==}
    case Some{True{}} None{} True{} False{}:
      {==}
    case Some{True{}} None{} True{} True{}:
      {==}
    case Some{True{}} Some{False{}} False{} False{}:
      {==}
    case Some{True{}} Some{False{}} False{} True{}:
      {==}
    case Some{True{}} Some{False{}} True{} False{}:
      P.absurd({P.implies(fires_value(Some{False{}}, False{}, False{}), fires_value(Some{True{}}, True{}, False{})) == True{} : Bool}, hx)
    case Some{True{}} Some{False{}} True{} True{}:
      {==}
    case Some{True{}} Some{True{}} False{} False{}:
      {==}
    case Some{True{}} Some{True{}} False{} True{}:
      {==}
    case Some{True{}} Some{True{}} True{} False{}:
      P.absurd({P.implies(fires_value(Some{True{}}, False{}, False{}), fires_value(Some{True{}}, True{}, False{})) == True{} : Bool}, hx)
    case Some{True{}} Some{True{}} True{} True{}:
      {==}

law fires_conservative:
  for +r: Refinement
  for +us: +List<Refinement>
  for hr: {valid(r) == True{} : Bool}
  for hu: {valid_all(us) == True{} : Bool}
  {P.implies(fires_after(r, us, False{}), fires_before(r, us, False{})) == True{} : Bool}
def fires_conservative(r, us, hr, hu):
  fire_conservative(before(r), after(r), excluded_before(us), excluded_after(us), opMatch_refine(r, hr), excluded_refine(us, hu))

law fires_ignore_value:
  for +v: +Maybe<Bool>
  for +x: Bool
  {fires_value(v, x, True{}) == Bool.and(definite(v), Bool.not(x)) : Bool}

def fires_ignore_value(v, x):
  match v x:
    case None{} False{}:
      {==}
    case None{} True{}:
      {==}
    case Some{False{}} False{}:
      {==}
    case Some{False{}} True{}:
      {==}
    case Some{True{}} False{}:
      {==}
    case Some{True{}} True{}:
      {==}

law fires_ignore:
  for +r: Refinement
  for +us: +List<Refinement>
  {fires_before(r, us, True{}) == Bool.and(definite(before(r)), Bool.not(excluded_before(us))) : Bool}
def fires_ignore(r, us):
  fires_ignore_value(before(r), excluded_before(us))
