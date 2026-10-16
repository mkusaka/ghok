package graphql

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/vektah/gqlparser/v2/ast"
)

// Text semantics are independent of curated operation identities.
func textKeys(field string) (titleKey, bodyKey string) {
	switch field {
	case "createPullRequest", "updatePullRequest", "createIssue", "updateIssue":
		return "title", "body"
	case "addComment", "updateIssueComment", "addPullRequestReview", "submitPullRequestReview", "updatePullRequestReview", "addPullRequestReviewComment", "updatePullRequestReviewComment", "addPullRequestReviewThread":
		return "", "body"
	case "mergePullRequest", "enablePullRequestAutoMerge":
		return "commitHeadline", "commitBody"
	case "dismissPullRequestReview":
		return "", "message"
	default:
		return "", ""
	}
}

func extractTexts(op *model.Operation, field *ast.Field, input map[string]any, req model.APIRequest, resolver *valueResolver) {
	titleKey, bodyKey := textKeys(field.Name)
	if titleKey == "" && bodyKey == "" {
		return
	}
	var inputValue *ast.Value
	for _, argument := range field.Arguments {
		if argument.Name == "input" {
			inputValue = argument.Value
		}
	}
	texts := &model.Texts{
		Title: inputText(op, input, titleKey, "title", inputValue, req, resolver),
		Body:  inputText(op, input, bodyKey, "body", inputValue, req, resolver),
	}
	if texts.Title != nil || texts.Body != nil {
		op.Texts = texts
	}
}

func inputText(op *model.Operation, input map[string]any, key, field string, inputValue *ast.Value, req model.APIRequest, resolver *valueResolver) *model.Text {
	if key == "" {
		return nil
	}
	text, reason := inputTextOrigin(inputValue, key, req, resolver)
	if value, ok := input[key].(string); ok {
		text.Value, text.Bytes = value, int64(len(value))
		return &text
	}
	if reason == "" || (text.Source != "file" && text.Source != "stdin") {
		return nil
	}
	text.Truncated = unavailableReason(reason) == model.UnknownInputTooLarge
	op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "texts." + field, Source: text.Source, Reason: reason})
	return &text
}

func inputTextOrigin(value *ast.Value, key string, req model.APIRequest, resolver *valueResolver) (model.Text, string) {
	if req.Input != "" {
		return fileTextOrigin(req.Input), ""
	}
	if value != nil && value.Kind == ast.ObjectValue {
		for _, child := range value.Children {
			if child.Name == key {
				return inputTextOrigin(child.Value, "", req, resolver)
			}
		}
	}
	if value != nil && value.Kind == ast.Variable {
		name := value.Raw
		if _, supplied := resolver.data.variables[name]; !supplied {
			if _, unavailable := resolver.data.unavailable[name]; !unavailable {
				for _, definition := range resolver.definition.VariableDefinitions {
					if definition.Variable == name && definition.DefaultValue != nil {
						return inputTextOrigin(definition.DefaultValue, key, req, resolver)
					}
				}
			}
		}
		fieldKey := name
		if req.Endpoint != "graphql" {
			fieldKey = "variables[" + name + "]"
		}
		if key != "" {
			fieldKey += "[" + key + "]"
		}
		for _, f := range req.Fields {
			if f.Key == fieldKey {
				if f.Typed && strings.HasPrefix(f.Value, "@") {
					return fileTextOrigin(strings.TrimPrefix(f.Value, "@")), resolver.data.unavailable[name].Reason
				}
				return model.Text{Source: "inline"}, ""
			}
		}
		return model.Text{Source: "inline"}, ""
	}
	// Literal text (including variable defaults) lives in the query document.
	for _, f := range req.Fields {
		if f.Key == "query" && f.Typed && strings.HasPrefix(f.Value, "@") {
			return fileTextOrigin(strings.TrimPrefix(f.Value, "@")), ""
		}
	}
	return model.Text{Source: "inline"}, ""
}

func fileTextOrigin(path string) model.Text {
	if path == "-" {
		return model.Text{Source: "stdin"}
	}
	return model.Text{Source: "file", Path: path}
}
