//go:build unix

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/logging"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/transforms"
)

type transformedRequest struct {
	invocation model.Invocation
	result     model.Result
	ids        []string
	results    []protocol.HookResult
	envBy      map[string]string
	envEdited  bool
	denial     *denial
	cleanup    func()
}

// Transforms are transactional across the whole chain: fail-open means the
// original request, not a partially transformed request.
func runTransforms(ctx context.Context, c *config.Config, runner *hooks.Runner, inv model.Invocation, result model.Result, real, eventID, via string) transformedRequest {
	out := transformedRequest{invocation: inv, result: result}
	originalEnv := runner.Env
	defer func() { runner.Env = originalEnv }()
	var cleanups []func()
	out.cleanup = func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
		cleanups = nil
	}
	for _, t := range c.Transforms {
		subject := policy.Subject{Result: out.result, Argv: out.result.Source.Argv, Phase: config.PhasePre, Token: policy.TokenKind(out.invocation.Env), Env: out.invocation.Env}
		yes, _ := policy.Matches(t.Match, t.Unless, subject)
		if !yes {
			yes, _ = policy.MatchMaybe(t.Match, t.Unless, subject)
		}
		if !yes {
			continue
		}
		input := hookInput(out.invocation, real, out.result, nil, policy.Evaluation{}, eventID)
		input.Invocation.Via = via
		input.Invocation.EnvChanged = out.envChanged()
		input.Policy = nil
		response, execution := runner.Transform(ctx, t, input)
		out.envEdited = out.envEdited || len(response.Env) > 0
		if execution.Outcome == protocol.OutcomeOK && (len(response.Set) > 0 || len(response.Unset) > 0 || len(response.Env) > 0) {
			next, normalized, cleanup, err := transforms.Apply(out.invocation, out.result, response)
			if err != nil {
				cleanup()
				execution.Outcome = protocol.OutcomeUnsupportedEdit
				if errors.Is(err, transforms.ErrInvalidResponse) {
					execution.Outcome = protocol.OutcomeInvalidResponse
				}
				execution.Error = err.Error()
				if runner.Redact != nil {
					execution.Error = runner.Redact(execution.Error)
				}
			} else {
				cleanups = append(cleanups, cleanup)
				envChanged := false
				for name, value := range response.Env {
					old, exists := out.invocation.Env[name]
					if value == nil && exists || value != nil && (!exists || old != *value) {
						envChanged = true
						if out.envBy == nil {
							out.envBy = make(map[string]string)
						}
						out.envBy[name] = t.ID
					} else if _, changed := out.envBy[name]; changed {
						out.envBy[name] = t.ID
					}
				}
				runner.Env = transformEnvironment(runner.Env, response.Env)
				if envChanged || !reflect.DeepEqual(next.Argv, out.invocation.Argv) || !bytes.Equal(next.Stdin.Data, out.invocation.Stdin.Data) {
					out.ids = append(out.ids, t.ID)
				}
				out.invocation, out.result = next, normalized
			}
		}
		if execution.Outcome != protocol.OutcomeOK {
			if t.OnError == config.OnErrorAllow {
				execution.Effective = protocol.DecisionAllow
				out.cleanup()
				out.invocation, out.result, out.ids = inv, result, nil
				out.envBy = nil
			} else {
				execution.Effective = protocol.DecisionDeny
				out.denial = &denial{Source: "transform", ID: t.ID, ErrorKind: string(execution.Outcome), Message: execution.Error}
			}
			out.results = append(out.results, execution)
			return out
		}
		out.results = append(out.results, execution)
	}
	return out
}

func (t *transformedRequest) envChanged() []string {
	if len(t.envBy) == 0 {
		return nil
	}
	names := make([]string, 0, len(t.envBy))
	for name := range t.envBy {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Preserve helper metadata and the already validated/incremented depth while
// moving serial transform commands to the current invocation environment.
func transformEnvironment(base []string, edits map[string]*string) []string {
	if len(edits) == 0 {
		return base
	}
	env := make([]string, 0, len(base)+len(edits))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, edited := edits[name]; !edited {
			env = append(env, entry)
		}
	}
	for name, value := range edits {
		if value != nil {
			env = append(env, name+"="+*value)
		}
	}
	return env
}

func environmentList(env map[string]string) []string {
	values := make([]string, 0, len(env))
	for name, value := range env {
		values = append(values, name+"="+value)
	}
	return values
}

func transformedLog(before, after model.Result, argv, ids, env []string, envBy map[string]string) *logging.Transformed {
	value := &logging.Transformed{Argv: argv, Transforms: ids, Texts: make(map[string]int64), Env: env, EnvBy: envBy}
	for i, op := range after.Operations {
		var old *model.Texts
		if i < len(before.Operations) {
			old = before.Operations[i].Texts
		}
		texts := func(t *model.Texts) [3]*model.Text {
			if t == nil {
				return [3]*model.Text{}
			}
			return [3]*model.Text{t.Title, t.Body, t.Notes}
		}
		previous, current := texts(old), texts(op.Texts)
		for j, name := range [3]string{"title", "body", "notes"} {
			a, b := previous[j], current[j]
			if a == nil && b == nil || a != nil && b != nil && a.Value == b.Value {
				continue
			}
			if b == nil {
				value.Texts[name] = 0
			} else {
				value.Texts[name] += int64(len(b.Value))
			}
		}
	}
	return value
}

type managementTransformed struct {
	Argv       []string          `json:"argv"`
	Transforms []string          `json:"transforms"`
	Operations []model.Operation `json:"operations"`
	Env        []string          `json:"env,omitempty"`
	EnvBy      map[string]string `json:"envBy,omitempty"`
	EnvUnset   []string          `json:"envUnset,omitempty"`
	TokenKind  string            `json:"tokenKind"`
}

func (a *managementAnalysis) transformed() *managementTransformed {
	if len(a.transformIDs) == 0 {
		return nil
	}
	value := &managementTransformed{Argv: a.redactor.Argv(a.result.Source.Argv), Operations: a.redactor.Result(a.result).Operations, TokenKind: policy.TokenKind(a.env)}
	for _, id := range a.transformIDs {
		value.Transforms = append(value.Transforms, a.redactor.String(id))
	}
	if len(a.envBy) > 0 {
		value.EnvBy = make(map[string]string, len(a.envBy))
	}
	for _, name := range a.envChanged {
		displayName := a.redactor.String(name)
		value.Env = append(value.Env, displayName)
		value.EnvBy[displayName] = a.redactor.String(a.envBy[name])
		if _, set := a.env[name]; !set {
			value.EnvUnset = append(value.EnvUnset, displayName)
		}
	}
	return value
}

func managementPrintTransformed(w io.Writer, a *managementAnalysis, showSecrets bool) {
	if len(a.transformIDs) == 0 {
		return
	}
	value := a.transformed()
	if showSecrets && !a.envEdited {
		value.Argv, value.Operations = a.result.Source.Argv, a.result.Operations
	}
	_, _ = fmt.Fprintln(w, "Transformed request:")
	_, _ = fmt.Fprintln(w, terminalText("  argv: "+managementCompact(value.Argv)))
	_, _ = fmt.Fprintln(w, terminalText("  transforms: "+managementCompact(value.Transforms)))
	if len(value.Env) > 0 {
		entries := make([]string, 0, len(value.Env))
		for i, name := range value.Env {
			if _, set := a.env[a.envChanged[i]]; set {
				entries = append(entries, name+" (set by "+value.EnvBy[name]+")")
			} else {
				entries = append(entries, name+" (unset)")
			}
		}
		_, _ = fmt.Fprintln(w, terminalText(a.redactor.String("  env: "+strings.Join(entries, ", "))))
	}
	_, _ = fmt.Fprintln(w, "  tokenKind: "+value.TokenKind)
	for i, op := range value.Operations {
		_, _ = fmt.Fprintln(w, terminalText(fmt.Sprintf("  operation %d: texts=%s parameters=%s target=%s", i, managementCompact(op.Texts), managementCompact(op.Parameters), managementCompact(op.Target))))
	}
}
