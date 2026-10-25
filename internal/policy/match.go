// Package policy evaluates normalized operations without I/O.
package policy

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

type Subject struct {
	Result  model.Result
	Argv    []string
	Context *model.ResolvedContext
	Phase   string
	// Outcome is how the real gh finished (post phase). nil means not known
	// yet: outcome criteria then match, so the hook stays a candidate.
	Outcome *protocol.ExecResult
	// Token is TokenKind of the invocation environment ("" is treated as none).
	Token string
	// Env is the caller's invocation environment, before hook-specific overrides.
	Env map[string]string
}

// outcomeMatches applies match.result / match.exitCode.
func outcomeMatches(m config.Match, o *protocol.ExecResult) bool {
	if o == nil || len(m.Results)+len(m.ExitCodes) == 0 {
		return true
	}
	failed := o.StartError != "" || o.Signal != "" || o.ExitCode != 0
	if len(m.Results) > 0 && !slices.ContainsFunc(m.Results, func(r string) bool {
		return r == config.ResultFailure && failed || r == config.ResultSuccess && !failed
	}) {
		return false
	}
	return len(m.ExitCodes) == 0 || o.StartError == "" && o.Signal == "" && slices.Contains(m.ExitCodes, o.ExitCode)
}

// MatchAction uses path.Match over action names. Dots are ordinary characters,
// so '*' spans multiple action segments. A family is not an implicit prefix:
// use 'pr.merge.*' to match its descendants, and 'pr.merge' for that exact action.
func MatchAction(pattern, action string) bool {
	ok, err := path.Match(pattern, action)
	return err == nil && ok
}
func MatchRepo(pattern, repo string) bool {
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(repo))
	return err == nil && ok
}

func considered(op model.Operation) bool {
	return op.Execution != model.ExecutionNotSelected && op.Execution != model.ExecutionSkipped
}
func anyMatch(patterns []string, value string, match func(string, string) bool) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if match(pattern, value) {
			return true
		}
	}
	return false
}
func exact(pattern, value string) bool { return pattern == value }

func invocationMatches(m config.Match, s Subject) bool {
	if !outcomeMatches(m, s.Outcome) {
		return false
	}
	for name, pattern := range m.Env {
		value := s.Env[name]
		if pattern == "*" {
			if value == "" {
				return false
			}
		} else if ok, err := path.Match(pattern, value); err != nil || !ok {
			return false
		}
	}
	token := s.Token
	if token == "" {
		token = TokenNone
	}
	if !anyMatch(m.Tokens, token, exact) {
		return false
	}
	if !anyMatch(m.Levels, string(s.Result.Classification.Level), exact) {
		return false
	}
	if len(m.Commands) == 0 {
		return true
	}
	for _, command := range m.Commands {
		prefix := strings.Fields(command)
		if len(prefix) == 0 || len(prefix) > len(s.Argv) {
			continue
		}
		yes := true
		for i, part := range prefix {
			if s.Argv[i] != part {
				yes = false
				break
			}
		}
		if yes {
			return true
		}
		// Normalized origins identify native command paths after global flags.
		for _, op := range s.Result.Operations {
			if !considered(op) || len(op.Origin.Command) < len(prefix) {
				continue
			}
			yes = true
			for i, part := range prefix {
				if op.Origin.Command[i] != part {
					yes = false
					break
				}
			}
			if yes {
				return true
			}
		}
	}
	return false
}

// RepositoryLess reports a classified command or REST operation gh runs without
// a repository (e.g. /rate_limit, /gists). Repository criteria never select it
// and it does not borrow the current repository. GraphQL documents and
// unclassified operations carry no such evidence and stay conservative.
func RepositoryLess(level model.Level, op model.Operation) bool {
	if level == model.LevelL3 || unclassified(op) || op.Family == model.FamilyUnknown || op.Target.Kind == "repository" || op.Target.Repository != "" || op.Target.NodeID != "" {
		return false
	}
	for _, u := range op.Unresolved {
		// An endpoint placeholder ({owner}, {repo}) is filled from the current repository.
		if u.Field == "target.repository" || u.Field == "target.owner" || u.Field == "endpoint" {
			return false
		}
	}
	return true
}

func targetValues(op model.Operation, s Subject) (repo, host string) {
	ctx := s.Context
	repo, host = op.Target.Repository, op.Target.Host
	unresolvedRepo, unresolvedHost := false, false
	for _, u := range op.Unresolved {
		if u.Field == "target.repository" {
			unresolvedRepo = true
		}
		if u.Field == "target.host" {
			unresolvedHost = true
		}
	}
	// An unresolved explicit target (particularly a GraphQL node ID) cannot be
	// replaced with the wrapper's current repository. Resolver updates targets.
	if ctx != nil {
		if repo == "" && !unresolvedRepo && op.Target.NodeID == "" && !RepositoryLess(s.Result.Classification.Level, op) {
			repo = ctx.Repository
		}
		if host == "" && !unresolvedHost {
			host = ctx.Host
		}
	}
	if unresolvedRepo {
		repo = ""
	}
	if unresolvedHost {
		host = ""
	}
	return
}

// parametersMatch applies match.parameters to scalar operation parameters
// using env-style globs; non-scalar values only satisfy "*".
func parametersMatch(m config.Match, op model.Operation) bool {
	for name, pattern := range m.Parameters {
		var text string
		switch v := op.Parameters[name].(type) {
		case nil:
		case string:
			text = v
		case bool, int, int64, float64, json.Number:
			text = fmt.Sprint(v)
		default:
			if pattern != "*" {
				return false
			}
			continue
		}
		if pattern == "*" {
			if text == "" {
				return false
			}
		} else if ok, err := path.Match(pattern, text); err != nil || !ok {
			return false
		}
	}
	return true
}

func opMatch(m config.Match, s Subject, op model.Operation) (yes, maybe bool) {
	if len(m.Methods) > 0 && (s.Result.Classification.Level != model.LevelL2 || !anyMatch(m.Methods, op.Origin.Method, exact)) {
		return false, false
	}
	if !considered(op) || !anyMatch(m.Operations, op.Action, MatchAction) || !anyMatch(m.Effects, string(op.Effect), exact) || !parametersMatch(m, op) {
		return false, false
	}
	repo, host := targetValues(op, s)
	if len(m.Repositories) != 0 {
		if RepositoryLess(s.Result.Classification.Level, op) {
			return false, false
		} else if repo == "" {
			maybe = true
		} else if !anyMatch(m.Repositories, repo, MatchRepo) {
			return false, false
		}
	}
	if len(m.Hosts) != 0 {
		if host == "" {
			maybe = true
		} else if !anyMatch(m.Hosts, host, MatchRepo) {
			return false, false
		}
	}
	return true, maybe
}

// invocationOnly reports whether m has no per-operation criteria.
func invocationOnly(m config.Match) bool {
	return len(m.Operations)+len(m.Methods)+len(m.Effects)+len(m.Repositories)+len(m.Hosts)+len(m.Parameters) == 0
}

// excluded reports whether an unless entry definitely matches op (nil: an
// invocation without operations). Maybe-matches never exclude, so unresolved
// targets and outcome criteria before gh has run keep the selection.
func excluded(unless []config.Match, s Subject, op *model.Operation) bool {
	for _, u := range unless {
		if s.Outcome == nil && len(u.Results)+len(u.ExitCodes) > 0 || !invocationMatches(u, s) {
			continue
		}
		if op == nil {
			if invocationOnly(u) {
				return true
			}
		} else if yes, maybe := opMatch(u, s, *op); yes && !maybe {
			return true
		}
	}
	return false
}

func matches(m config.Match, unless []config.Match, s Subject, wantMaybe bool) (bool, []int) {
	if !invocationMatches(m, s) {
		return false, nil
	}
	var indices []int
	for i, op := range s.Result.Operations {
		yes, maybe := opMatch(m, s, op)
		if yes && maybe == wantMaybe && !excluded(unless, s, &op) {
			indices = append(indices, i)
		}
	}
	if len(indices) > 0 {
		return true, indices
	}
	// Invocation-only hooks can run when a mutation-free result has no ops.
	if !wantMaybe && len(s.Result.Operations) == 0 && invocationOnly(m) && !excluded(unless, s, nil) {
		return true, nil
	}
	return false, nil
}

// Matches returns definite matches only, minus operations excluded by unless.
// Criteria must hold on the same op.
func Matches(m config.Match, unless []config.Match, s Subject) (bool, []int) {
	return matches(m, unless, s, false)
}

// MatchMaybe returns only candidates whose repo/host criteria are unresolved.
// Use together with Matches for conservative, fail-closed selection.
func MatchMaybe(m config.Match, unless []config.Match, s Subject) (bool, []int) {
	return matches(m, unless, s, true)
}

// UnlessMaybe reports whether an unless entry maybe-matches operation index:
// resolving its repository/host could exclude the operation.
func UnlessMaybe(unless []config.Match, s Subject, index int) (repo, host bool) {
	for _, u := range unless {
		if _, indexes := MatchMaybe(u, nil, s); slices.Contains(indexes, index) {
			repo, host = repo || len(u.Repositories) > 0, host || len(u.Hosts) > 0
		}
	}
	return
}
