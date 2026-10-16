package rest

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

// Request body fields, rather than the combined parameter view, carry text.
func extractTexts(op *model.Operation, operationID string, req model.APIRequest, body map[string]any) {
	var titleKey, bodyKey, notesKey string
	switch operationID {
	case "pulls/create", "pulls/update", "issues/create", "issues/update":
		titleKey, bodyKey = "title", "body"
	case "issues/create-comment", "issues/update-comment", "pulls/create-review-comment", "pulls/update-review-comment", "pulls/create-reply-for-review-comment", "pulls/create-review", "pulls/update-review", "pulls/submit-review", "gists/create-comment", "gists/update-comment", "repos/create-commit-comment", "repos/update-commit-comment":
		bodyKey = "body"
	case "pulls/merge":
		titleKey, bodyKey = "commit_title", "commit_message"
	case "repos/create-release", "repos/update-release":
		titleKey, notesKey = "name", "body"
	case "pulls/dismiss-review":
		bodyKey = "message"
	default:
		return
	}
	texts := &model.Texts{}
	texts.Title = bodyText(op, req, body, titleKey, "title")
	texts.Body = bodyText(op, req, body, bodyKey, "body")
	texts.Notes = bodyText(op, req, body, notesKey, "notes")
	if texts.Title != nil || texts.Body != nil || texts.Notes != nil {
		op.Texts = texts
	}
}

func bodyText(op *model.Operation, req model.APIRequest, body map[string]any, key, field string) *model.Text {
	if key == "" || strings.EqualFold(req.Method, "GET") {
		return nil
	}
	if body != nil {
		if op.Editable == nil {
			op.Editable = make(map[string]model.EditLocator)
		}
		loc := model.EditLocator{Kind: "api-field", Key: []string{key}, Parameter: key, Text: field}
		if req.Input != "" {
			loc.Kind, loc.Input = "json", req.Input
		}
		op.Editable["texts/"+field+"/value"] = loc
		op.Editable["parameters/"+key] = loc
	}
	text := model.Text{Source: "inline"}
	if req.Input != "" {
		text.Source, text.Path = "file", req.Input
		if req.Input == "-" {
			text.Source, text.Path = "stdin", ""
		}
	} else {
		for _, f := range req.Fields {
			if f.Key == key && f.Typed && strings.HasPrefix(f.Value, "@") {
				text.Source, text.Path = "file", strings.TrimPrefix(f.Value, "@")
				if text.Path == "-" {
					text.Source, text.Path = "stdin", ""
				}
			}
		}
		for _, u := range op.Unresolved {
			if u.Field != "parameters."+key {
				continue
			}
			if u.Source != "file" && u.Source != "stdin" {
				return nil
			}
			text.Truncated = textTooLarge(u.Reason)
			op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "texts." + field, Source: u.Source, Reason: u.Reason})
			return &text
		}
	}
	value, ok := body[key].(string)
	if !ok {
		return nil
	}
	text.Value, text.Bytes = value, int64(len(value))
	return &text
}

func textTooLarge(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "too large") || strings.Contains(reason, "too-large") || strings.Contains(reason, "size limit") || strings.Contains(reason, "exceeds")
}
