package rest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
)

func buildParameters(inv model.Invocation, req model.APIRequest, query url.Values) (map[string]any, map[string]any, []model.Unresolved, bool, error) {
	var unresolved []model.Unresolved
	fields := req
	fields.Fields = append([]model.APIField(nil), req.Fields...)
	stdinConsumed := false
	for i, f := range fields.Fields {
		if !f.Typed {
			continue
		}
		if f.Value == "@-" && inv.Stdin.Status == model.StdinCaptured {
			if stdinConsumed {
				fields.Fields[i].Value, fields.Fields[i].Typed = "", false
			}
			stdinConsumed = true
		} else if !strings.HasPrefix(f.Value, "@") && hasPlaceholder(f.Value) {
			value, us := expandPlaceholders(inv, f.Value, "parameters."+f.Key)
			fields.Fields[i].Value = value
			// gh expands placeholders after trying integer/bool conversion.
			// A repository named "123" therefore remains a string.
			fields.Fields[i].Typed = false
			unresolved = append(unresolved, us...)
		}
	}
	params, fieldUnresolved, err := apiargs.BuildParams(fields, inv)
	for _, u := range fieldUnresolved {
		u.Field = "parameters." + strings.TrimPrefix(u.Field, "fields.")
		unresolved = append(unresolved, u)
	}
	if err != nil {
		return params, nil, unresolved, false, err
	}
	body, bodyKnown := params, len(unresolved) == 0
	if strings.EqualFold(req.Method, "GET") || req.Input != "" {
		addQueryFields(query, "", params)
		body = nil
	}
	if req.Input != "" {
		var data []byte
		var inputErr error
		source := "file"
		if req.Input == "-" {
			source = "stdin"
			if inv.Stdin.Status != model.StdinCaptured {
				inputErr = fmt.Errorf("stdin not captured (%s)", inv.Stdin.Status)
			} else if !stdinConsumed {
				data = inv.Stdin.Data
			}
		} else if inv.ReadFile == nil {
			inputErr = fmt.Errorf("input file reader unavailable")
		} else {
			data, inputErr = inv.ReadFile(req.Input)
		}
		if inputErr != nil {
			bodyKnown = false
			unresolved = append(unresolved, model.Unresolved{Field: "parameters", Reason: inputErr.Error(), Source: source})
		} else if json.Unmarshal(data, &body) != nil || body == nil {
			body, bodyKnown = nil, false
			unresolved = append(unresolved, model.Unresolved{Field: "parameters", Reason: "input body is not a JSON object", Source: source})
		} else {
			bodyKnown = true
		}
	}
	combined := make(map[string]any, len(query)+len(body))
	for key, values := range query {
		if len(values) == 1 {
			combined[key] = values[0]
		} else {
			combined[key] = append([]string(nil), values...)
		}
	}
	// Body values take precedence in the canonical parameter view. Route
	// specialization consults body only, never query values on a write.
	for key, value := range body {
		combined[key] = value
	}
	return combined, body, unresolved, bodyKnown, nil
}

// Port of gh's addQueryParam: nested map keys are flattened, arrays use [].
// GET values are strings on the wire even when -F originally supplied types.
func addQueryFields(query url.Values, key string, value any) {
	switch v := value.(type) {
	case map[string]any:
		for k, entry := range v {
			addQueryFields(query, k, entry)
		}
	case []any:
		for _, entry := range v {
			addQueryFields(query, key+"[]", entry)
		}
	case string:
		query.Add(key, v)
	case int:
		query.Add(key, strconv.Itoa(v))
	case bool:
		query.Add(key, strconv.FormatBool(v))
	case nil:
		query.Add(key, "")
	}
}

func specialize(op *model.Operation, body map[string]any, bodyKnown bool) {
	switch op.Action {
	case "pr.edit", "issue.edit":
		prefix := strings.TrimSuffix(op.Action, ".edit")
		switch body["state"] {
		case "closed":
			op.Family, op.Action = prefix+".close", prefix+".close"
		case "open":
			op.Family, op.Action = prefix+".reopen", prefix+".reopen"
		}
	case "repo.edit":
		if body["archived"] == true {
			op.Family, op.Action = "repo.archive", "repo.archive"
		}
	case "repo.create":
		name, _ := body["name"].(string)
		if op.Target.Owner == "" {
			op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "target.owner", Reason: "authenticated user is not known", Source: "api"})
		}
		if name != "" && op.Target.Owner != "" && !hasPlaceholder(name) {
			op.Target.Repository = op.Target.Owner + "/" + name
		} else {
			op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "target.repository", Reason: "new repository owner or name is unavailable", Source: "input"})
		}
	case "pr.merge.direct":
		if value, ok := body["merge_method"]; ok {
			op.Parameters["method"] = value
			delete(op.Parameters, "merge_method")
			for i := range op.Unresolved {
				if op.Unresolved[i].Field == "parameters.merge_method" {
					op.Unresolved[i].Field = "parameters.method"
				}
			}
		}
	case "pr.review.create", "pr.review.submit":
		base := op.Action
		_, eventPresent := body["event"]
		if event, ok := body["event"].(string); ok {
			switch event {
			case "APPROVE":
				op.Action = "pr.review.approve"
			case "REQUEST_CHANGES":
				op.Action = "pr.review.request-changes"
			case "COMMENT":
				op.Action = "pr.review.comment"
			default:
				op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "parameters.event", Reason: "unrecognized review event", Source: "input"})
			}
		} else if bodyKnown && !eventPresent && base == "pr.review.create" {
			op.Action = "pr.review.pending"
		} else {
			op.Unresolved = appendUnresolved(op.Unresolved, model.Unresolved{Field: "parameters.event", Reason: "review event is unavailable or invalid", Source: "input"})
		}
	}
	for _, key := range []string{"ref", "sha", "base", "head"} {
		v, ok := body[key].(string)
		if !ok || hasPlaceholder(v) {
			continue
		}
		switch {
		case key == "ref" && (op.Family == "ref.create" || op.Family == "workflow.dispatch"):
			op.Target.Ref = v
		case key == "sha" && strings.HasPrefix(op.Family, "ref."):
			op.Target.SHA = v
		case key == "base" && (strings.HasPrefix(op.Family, "pr.") || op.Family == "branch.merge"):
			op.Target.BaseBranch = v
		case key == "head" && (strings.HasPrefix(op.Family, "pr.") || op.Family == "branch.merge"):
			op.Target.HeadBranch = v
		}
	}
}
