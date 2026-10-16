package graphql

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/vektah/gqlparser/v2/ast"
)

// Only explicitly transported variables can be rewritten. A single expanded
// use guarantees that no other root, argument or directive consumes the edit.
func extractEditable(op *model.Operation, field *ast.Field, input map[string]any, req model.APIRequest, resolver *valueResolver, uses map[string]int) {
	if input == nil || uses == nil {
		return
	}
	var value *ast.Value
	for _, argument := range field.Arguments {
		if argument.Name == "input" {
			value = argument.Value
		}
	}
	if value == nil {
		return
	}
	titleKey, bodyKey := textKeys(field.Name)
	record := func(key string, locator model.EditLocator, text bool) {
		locator.Parameter = key
		if text {
			switch key {
			case titleKey:
				locator.Text = "title"
			case bodyKey:
				locator.Text = "body"
			}
		}
		if op.Editable == nil {
			op.Editable = make(map[string]model.EditLocator)
		}
		pointerKey := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		op.Editable["parameters/"+pointerKey] = locator
		if locator.Text != "" {
			op.Editable["texts/"+locator.Text+"/value"] = locator
		}
	}
	if value.Kind == ast.Variable {
		transport, supplied := resolver.data.variables[value.Raw].(map[string]any)
		if !supplied || uses[value.Raw] != 1 {
			return
		}
		for key, current := range transport {
			locator, ok := variableLocator(value.Raw, key, current, req)
			if ok {
				_, text := current.(string)
				record(key, locator, text)
			}
		}
		if req.Input != "" {
			// A supplied JSON input object can also carry newly inserted fields.
			locator, _ := variableLocator(value.Raw, "*", nil, req)
			record("*", locator, false)
			for _, key := range []string{titleKey, bodyKey} {
				if key == "" {
					continue
				}
				if _, exists := transport[key]; !exists {
					locator, _ := variableLocator(value.Raw, key, nil, req)
					record(key, locator, true)
				}
			}
		}
		return
	}
	if value.Kind != ast.ObjectValue {
		return
	}
	for _, child := range value.Children {
		variable := child.Value
		if variable == nil || variable.Kind != ast.Variable || uses[variable.Raw] != 1 {
			continue
		}
		current, supplied := resolver.data.variables[variable.Raw]
		if !supplied {
			continue
		}
		locator, ok := variableLocator(variable.Raw, "", current, req)
		if ok {
			_, text := current.(string)
			record(child.Name, locator, text)
		}
	}
}

func variableLocator(variable, key string, value any, req model.APIRequest) (model.EditLocator, bool) {
	if req.Input != "" {
		path := []string{"variables", variable}
		if key != "" {
			path = append(path, key)
		}
		return model.EditLocator{Kind: "json", Input: req.Input, Key: path}, true
	}
	// Nested and repeated fields transport containers too. Missing transports
	// must not be invented as API flags.
	switch value.(type) {
	case string, bool, int, int64, float64, []any, []string, map[string]any:
	default:
		return model.EditLocator{}, false
	}
	path := []string{variable}
	if req.Endpoint != "graphql" {
		path = []string{"variables", variable}
	}
	if key != "" {
		path = append(path, key)
	}
	fieldKey := path[0]
	for _, part := range path[1:] {
		fieldKey += "[" + part + "]"
	}
	for _, field := range req.Fields {
		if (field.Key == fieldKey && !field.NoValue) || strings.HasPrefix(field.Key, fieldKey+"[") {
			return model.EditLocator{Kind: "api-field", Key: path}, true
		}
	}
	return model.EditLocator{}, false
}
