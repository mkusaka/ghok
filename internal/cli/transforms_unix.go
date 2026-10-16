//go:build unix

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/logging"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
	"github.com/mkusaka/ghok/internal/transforms"
)

type transformedRequest struct {
	invocation model.Invocation
	result     model.Result
	ids        []string
	results    []protocol.HookResult
	denial     *denial
	cleanup    func()
}

// Transforms are transactional across the whole chain: fail-open means the
// original request, not a partially transformed request.
func runTransforms(ctx context.Context, c *config.Config, runner *hooks.Runner, inv model.Invocation, result model.Result, real, eventID, via string) transformedRequest {
	out := transformedRequest{invocation: inv, result: result}
	var cleanups []func()
	out.cleanup = func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
		cleanups = nil
	}
	for _, t := range c.Transforms {
		subject := policy.Subject{Result: out.result, Argv: out.result.Source.Argv, Phase: config.PhasePre, Token: policy.TokenKind(inv.Env), Env: inv.Env}
		yes, _ := policy.Matches(t.Match, subject)
		if !yes {
			yes, _ = policy.MatchMaybe(t.Match, subject)
		}
		if !yes {
			continue
		}
		input := hookInput(out.invocation.Argv, inv.Cwd, real, out.result, nil, policy.Evaluation{}, eventID)
		input.Invocation.Via = via
		input.Policy = nil
		response, execution := runner.Transform(ctx, t, input)
		if execution.Outcome == protocol.OutcomeOK && (len(response.Set) > 0 || len(response.Unset) > 0) {
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
				if !reflect.DeepEqual(next.Argv, out.invocation.Argv) || !bytes.Equal(next.Stdin.Data, out.invocation.Stdin.Data) {
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

func transformedLog(before, after model.Result, argv, ids []string) *logging.Transformed {
	value := &logging.Transformed{Argv: argv, Transforms: ids, Texts: make(map[string]int64)}
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
}

func (a *managementAnalysis) transformed() *managementTransformed {
	if len(a.transformIDs) == 0 {
		return nil
	}
	r := redact.New(a.env)
	return &managementTransformed{Argv: r.Argv(a.result.Source.Argv), Transforms: a.transformIDs, Operations: r.Result(a.result).Operations}
}

func managementPrintTransformed(w io.Writer, a *managementAnalysis, showSecrets bool) {
	if len(a.transformIDs) == 0 {
		return
	}
	value := a.transformed()
	if showSecrets {
		value = &managementTransformed{Argv: a.result.Source.Argv, Transforms: a.transformIDs, Operations: a.result.Operations}
	}
	_, _ = fmt.Fprintln(w, "Transformed request:")
	_, _ = fmt.Fprintln(w, terminalText("  argv: "+managementCompact(value.Argv)))
	_, _ = fmt.Fprintln(w, terminalText("  transforms: "+managementCompact(value.Transforms)))
	for i, op := range value.Operations {
		_, _ = fmt.Fprintln(w, terminalText(fmt.Sprintf("  operation %d: texts=%s parameters=%s target=%s", i, managementCompact(op.Texts), managementCompact(op.Parameters), managementCompact(op.Target))))
	}
}
