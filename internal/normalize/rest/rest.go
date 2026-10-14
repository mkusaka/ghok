// Package rest is the L2 normalizer for `gh api` REST calls.
package rest

import (
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

// Normalize classifies a parsed non-GraphQL `gh api` request. All input comes
// from inv; it never consults the process environment, git, or the network.
func Normalize(inv model.Invocation, req model.APIRequest) (res model.Result) {
	// BuildParams is shared code and accepts arbitrary field key syntax. Keep
	// its malformed-input panics (and injected reader panics) from escaping.
	defer func() {
		if recover() != nil {
			res = model.NewUnknown(model.LevelL2, model.UnknownParseError, "unable to parse REST request")
			res.Source = model.Source{Kind: "rest", Method: req.Method, Argv: inv.Argv}
		}
	}()
	method := strings.ToUpper(req.Method)
	res = model.Result{
		SchemaVersion: model.SchemaVersion,
		Classification: model.Classification{
			Level: model.LevelL2, Confidence: model.ConfidenceExact, Resolution: model.ResolutionComplete,
		},
		Source: model.Source{Kind: "rest", Method: method, Argv: inv.Argv},
	}
	ep, err := parseEndpoint(inv, req)
	if err != nil {
		res = model.NewUnknown(model.LevelL2, model.UnknownMalformed, err.Error())
		res.Source = model.Source{Kind: "rest", Method: method, Argv: inv.Argv}
		res.Operations[0].Origin.Method = method
		return res
	}
	res.Source.Path, res.Source.Host = ep.path, ep.host
	op := model.Operation{
		Target: model.Target{Host: ep.host}, Execution: model.ExecutionWillRun,
		Origin: model.Origin{Method: method}, Unresolved: ep.unresolved,
	}
	var rt *route
	var captures map[string]string
	if !ep.opaque {
		rt, captures = matchRoute(method, ep.segments)
	}
	if rt != nil {
		op.Family, op.Action, op.Effect = rt.family, rt.action, rt.effect
		op.Origin.Route = rt.path
		extractTarget(&op, captures)
	} else {
		op.Family, op.Action = model.FamilyUnknown, "api."+strings.ToLower(method)+".unknown"
		op.UnknownReason = model.UnknownEndpoint
		op.Effect = methodEffect(method)
		res.Classification.Confidence = model.ConfidenceNone
		if op.Effect == model.EffectRead {
			op.Family, op.Action, op.UnknownReason = "api.read", "api.read.unknown", model.UnknownNone
			op.Detail = "unregistered route; GitHub REST GET/HEAD requests are side-effect free by convention"
			res.Classification.Confidence = model.ConfidenceHeuristic
		}
		// A repository is still useful to policy on an unregistered route.
		if len(ep.segments) >= 3 && ep.segments[0] == "repos" &&
			validCapture("owner", ep.segments[1]) && validCapture("repo", ep.segments[2]) {
			extractTarget(&op, map[string]string{"owner": ep.segments[1], "repo": ep.segments[2]})
		}
		if ep.opaque {
			op.Family, op.Action, op.Effect = model.FamilyUnknown, "api."+strings.ToLower(method)+".unknown", model.EffectUnknown
			op.UnknownReason = model.UnknownEndpoint
			op.Detail = "absolute URL is not a recognized GitHub REST API URL"
			res.Classification.Confidence = model.ConfidenceNone
		}
	}
	params, body, unresolved, bodyKnown, paramErr := buildParameters(inv, req, ep.query)
	op.Parameters = params
	op.Unresolved = append(op.Unresolved, unresolved...)
	for key, value := range captures {
		switch key {
		case "owner", "repo", "org", "pull_number", "issue_number", "branch", "ref":
		default:
			op.Parameters[key] = value
		}
	}
	if rt != nil {
		specialize(&op, body, bodyKnown)
	}
	if paramErr != nil {
		malformed(&op, "gh cannot construct request fields: "+paramErr.Error())
		res.Errors = append(res.Errors, paramErr.Error())
		res.Classification.Confidence = model.ConfidenceNone
	}
	for _, h := range req.Headers {
		name, _, ok := strings.Cut(h, ":")
		if !ok {
			malformed(&op, "gh rejects a header without ':'")
			res.Classification.Confidence = model.ConfidenceNone
			continue
		}
		if strings.EqualFold(name, "X-HTTP-Method-Override") {
			op.Parameters["headers"] = []string{"X-HTTP-Method-Override"}
			op.Family, op.Action, op.Effect = model.FamilyUnknown, "api."+strings.ToLower(method)+".unknown", model.EffectUnknown
			op.UnknownReason = model.UnknownOperationAmbig
			op.Detail = "X-HTTP-Method-Override may change the server-side method; header value is not recorded"
			res.Classification.Confidence = model.ConfidenceHeuristic
		}
	}
	if req.Paginate && ((req.MethodExplicit && method != "GET") || req.Input != "") {
		malformed(&op, "gh rejects REST --paginate with explicit non-GET methods or --input; intended action: "+op.Action)
		res.Classification.Confidence = model.ConfidenceNone
	}
	if req.Paginate && !req.MethodExplicit && method == "POST" && req.Input == "" {
		op.Detail = "gh validates --paginate before inferring POST from fields; the initial write is still sent"
	}
	res.Classification.MutationFree = op.Effect == model.EffectRead
	if len(op.Unresolved) > 0 {
		res.Classification.Resolution = model.ResolutionDeferred
		for _, u := range op.Unresolved {
			if u.Source != "git" && u.Source != "git/config" {
				res.Classification.Resolution = model.ResolutionPartial
				break
			}
		}
	}
	res.Operations = []model.Operation{op}
	return res
}

func methodEffect(method string) model.Effect {
	switch method {
	case "GET", "HEAD":
		return model.EffectRead
	case "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT":
		return model.EffectWrite
	default:
		return model.EffectUnknown
	}
}

func malformed(op *model.Operation, detail string) {
	op.Family, op.Action, op.Effect = model.FamilyUnknown, "api."+strings.ToLower(op.Origin.Method)+".unknown", model.EffectUnknown
	op.UnknownReason, op.Detail = model.UnknownMalformed, detail
	op.Execution, op.Evidence = model.ExecutionSkipped, "gh rejects this request before sending HTTP"
}

func extractTarget(op *model.Operation, c map[string]string) {
	for _, pair := range [][2]string{
		{"owner", "target.owner"}, {"repo", "target.repository"}, {"branch", "target.branch"}, {"ref", "target.ref"},
	} {
		key, field := pair[0], pair[1]
		v := c[key]
		if v == "" {
			continue
		}
		if hasPlaceholder(v) {
			op.Unresolved = appendUnresolved(op.Unresolved, model.Unresolved{Field: field, Reason: "repository placeholder requires git", Source: "git"})
			continue
		}
		switch key {
		case "owner":
			op.Target.Owner = v
		case "repo":
			if c["owner"] != "" && !hasPlaceholder(c["owner"]) {
				op.Target.Repository = c["owner"] + "/" + v
			} else {
				op.Unresolved = appendUnresolved(op.Unresolved, model.Unresolved{Field: field, Reason: "repository owner requires git", Source: "git"})
			}
		case "branch":
			op.Target.Branch = v
		case "ref":
			op.Target.Ref = v
		}
	}
	if c["org"] != "" && op.Target.Owner == "" {
		op.Target.Owner = c["org"]
	}
	if c["sha"] != "" {
		op.Target.SHA = c["sha"]
	}
	for _, key := range []string{"pull_number", "issue_number"} {
		if v := c[key]; v != "" {
			// The matcher already checked positive decimal syntax and overflow.
			n, _ := strconv.Atoi(v)
			op.Target.Number = model.IntPtr(n)
		}
	}
}
