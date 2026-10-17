package redact

import (
	"encoding/base64"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

// Result deep-copies every mutable field and redacts all strings, not only the
// usual credential locations. Classification and operation meaning are
// otherwise preserved; redacted GraphQL is a display copy, never replay input.
func (r *Redactor) Result(res model.Result) model.Result {
	out := res
	out.Classification = r.classification(res.Classification)
	out.Operations = r.operations(res.Operations)
	out.Errors = r.strings(res.Errors)
	out.Source.Kind = r.String(res.Source.Kind)
	out.Source.Argv = r.Argv(res.Source.Argv)
	out.Source.Alias = r.String(res.Source.Alias)
	out.Source.Method = r.String(res.Source.Method)
	out.Source.Path = r.String(res.Source.Path)
	out.Source.Host = r.String(res.Source.Host)
	out.Source.GraphQL = r.graphQL(res.Source.GraphQL)
	return out
}

func (r *Redactor) classification(in model.Classification) model.Classification {
	in.Level = model.Level(r.String(string(in.Level)))
	in.Confidence = model.Confidence(r.String(string(in.Confidence)))
	in.Resolution = model.Resolution(r.String(string(in.Resolution)))
	return in
}

func (r *Redactor) operations(in []model.Operation) []model.Operation {
	if in == nil {
		return nil
	}
	out := make([]model.Operation, len(in))
	for i, op := range in {
		out[i] = op
		out[i].Family = r.String(op.Family)
		out[i].Action = r.String(op.Action)
		out[i].Effect = model.Effect(r.String(string(op.Effect)))
		out[i].Target = r.target(op.Target)
		if op.Parameters != nil {
			out[i].Parameters = r.Value(op.Parameters).(map[string]any)
		}
		out[i].Texts = r.texts(op.Texts)
		out[i].Execution = model.Execution(r.String(string(op.Execution)))
		out[i].Evidence = r.String(op.Evidence)
		out[i].Unresolved = r.unresolved(op.Unresolved)
		out[i].UnknownReason = model.UnknownReason(r.String(string(op.UnknownReason)))
		out[i].Detail = r.String(op.Detail)
		out[i].Origin.Command = r.strings(op.Origin.Command)
		out[i].Origin.Method = r.String(op.Origin.Method)
		out[i].Origin.Route = r.String(op.Origin.Route)
		out[i].Origin.Field = r.String(op.Origin.Field)
		out[i].Origin.Alias = r.String(op.Origin.Alias)
		out[i].Origin.Operation = r.String(op.Origin.Operation)
		out[i].Origin.OpType = r.String(op.Origin.OpType)
	}
	return out
}

func (r *Redactor) texts(in *model.Texts) *model.Texts {
	if in == nil {
		return nil
	}
	return &model.Texts{Title: r.text(in.Title), Body: r.text(in.Body), Notes: r.text(in.Notes)}
}

func (r *Redactor) text(in *model.Text) *model.Text {
	if in == nil {
		return nil
	}
	out := *in
	out.Redactions = r.WithCount(func(r *Redactor) {
		out.Value = r.String(in.Value)
	})
	out.Source = r.String(in.Source)
	out.Path = r.String(in.Path)
	out.Status = model.StdinStatus(r.String(string(in.Status)))
	return &out
}

func (r *Redactor) target(in model.Target) model.Target {
	out := in
	out.Kind = r.String(in.Kind)
	out.Host = r.String(in.Host)
	out.Owner = r.String(in.Owner)
	out.Repository = r.String(in.Repository)
	if in.Number != nil {
		n := *in.Number
		out.Number = &n
	}
	out.NodeID = r.String(in.NodeID)
	out.Branch = r.String(in.Branch)
	out.BaseBranch = r.String(in.BaseBranch)
	out.HeadBranch = r.String(in.HeadBranch)
	out.SHA = r.String(in.SHA)
	out.Ref = r.String(in.Ref)
	out.URL = r.String(in.URL)
	return out
}

func (r *Redactor) unresolved(in []model.Unresolved) []model.Unresolved {
	if in == nil {
		return nil
	}
	out := make([]model.Unresolved, len(in))
	for i, u := range in {
		out[i] = model.Unresolved{Field: r.String(u.Field), Reason: r.String(u.Reason), Source: r.String(u.Source)}
	}
	return out
}

// HookInput makes a display-only redacted copy, preserving the counters that
// describe substitutions in the actual hook payload. Use WithCount to count
// additional display masking independently.
func (r *Redactor) HookInput(in protocol.Input) protocol.Input {
	out := r.input(in)
	for i, op := range in.Operations {
		if op.Texts == nil {
			continue
		}
		sources := [3]*model.Text{op.Texts.Title, op.Texts.Body, op.Texts.Notes}
		targets := [3]*model.Text{out.Operations[i].Texts.Title, out.Operations[i].Texts.Body, out.Operations[i].Texts.Notes}
		for j, source := range sources {
			if source != nil {
				targets[j].Redactions = source.Redactions
			}
		}
	}
	return out
}

// Input makes a redacted deep copy of an entire hook payload. Captured output
// is decoded, redacted (including JSON keys/GraphQL literals), then re-encoded;
// original byte counts and truncation metadata continue to describe execution.
func (r *Redactor) Input(in protocol.Input) protocol.Input {
	var out protocol.Input
	count := r.WithCount(func(r *Redactor) {
		out = r.input(in)
	})
	out.Redactions = count
	return out
}

func (r *Redactor) input(in protocol.Input) protocol.Input {
	out := in
	out.Event = r.String(in.Event)
	out.EventID = r.String(in.EventID)
	out.HookID = r.String(in.HookID)
	out.Invocation.Argv = r.Argv(in.Invocation.Argv)
	out.Invocation.Alias = r.String(in.Invocation.Alias)
	out.Invocation.Expanded = r.Argv(in.Invocation.Expanded)
	out.Invocation.Cwd = r.String(in.Invocation.Cwd)
	out.Invocation.RealGh = r.String(in.Invocation.RealGh)
	out.Invocation.TokenKind = r.String(in.Invocation.TokenKind)
	out.Invocation.EnvChanged = r.strings(in.Invocation.EnvChanged)
	out.Classification = r.classification(in.Classification)
	out.Operations = r.operations(in.Operations)
	out.Errors = r.strings(in.Errors)
	if in.Context != nil {
		context := *in.Context
		out.Context = &context
		context.Host = r.String(context.Host)
		context.Owner = r.String(context.Owner)
		context.Repository = r.String(context.Repository)
		context.Branch = r.String(context.Branch)
		context.HeadSHA = r.String(context.HeadSHA)
		context.GitDir = r.String(context.GitDir)
		if context.Sources != nil {
			context.Sources = r.Value(context.Sources).(map[string]string)
		}
		context.Unresolved = r.unresolved(context.Unresolved)
	}
	if in.Policy != nil {
		policy := *in.Policy
		out.Policy = &policy
		policy.Mode = r.String(policy.Mode)
		policy.Decision = protocol.Decision(r.String(string(policy.Decision)))
		policy.Reasons = r.strings(policy.Reasons)
		policy.Rules = r.strings(policy.Rules)
	}
	if in.Result != nil {
		result := *in.Result
		out.Result = &result
		result.Signal = r.String(result.Signal)
		result.StartError = r.String(result.StartError)
		result.Stdout = r.output(result.Stdout)
		result.Stderr = r.output(result.Stderr)
	}
	if in.Previous != nil {
		out.Previous = make([]protocol.HookResult, len(in.Previous))
		for i, hook := range in.Previous {
			out.Previous[i] = hook
			out.Previous[i].HookID = r.String(hook.HookID)
			out.Previous[i].Phase = r.String(hook.Phase)
			out.Previous[i].Outcome = protocol.Outcome(r.String(string(hook.Outcome)))
			out.Previous[i].Decision = protocol.Decision(r.String(string(hook.Decision)))
			out.Previous[i].Reason = r.String(hook.Reason)
			out.Previous[i].Message = r.String(hook.Message)
			out.Previous[i].Effective = protocol.Decision(r.String(string(hook.Effective)))
			out.Previous[i].Error = r.String(hook.Error)
			out.Previous[i].Stderr = r.String(hook.Stderr)
		}
	}
	return out
}

func (r *Redactor) output(in *protocol.Output) *protocol.Output {
	if in == nil {
		return nil
	}
	out := *in
	body, err := base64.StdEncoding.DecodeString(in.Base64)
	if err != nil {
		// Never pass partially decoded or opaque invalid data through: it can
		// contain credentials that String cannot see in their encoded form.
		body = []byte(r.mask(in.Base64))
	} else {
		body = r.body(body)
	}
	out.Base64 = base64.StdEncoding.EncodeToString(body)
	return &out
}
