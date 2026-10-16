// Package graphql is the L3 normalizer for `gh api graphql` (AST based).
package graphql

import (
	"fmt"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Normalize classifies a parsed GraphQL request without ambient I/O.
func Normalize(inv model.Invocation, req model.APIRequest) model.Result {
	data, err := reconstruct(inv, req)
	source := sourceFor(inv, req, data.query)
	if err != nil {
		return unknownResult(source, err)
	}
	if req.Endpoint != "graphql" && strings.Trim(req.Endpoint, "/") == "graphql" {
		switch {
		case source.Host == "":
			data.note = joinText(data.note, "relative GraphQL endpoint: host is unresolved; GraphQL routing depends on the GitHub instance")
		case source.Host != "github.com" && source.Host != "ghe.com" && !strings.HasSuffix(source.Host, ".ghe.com"):
			return unknownResult(source, fail(model.UnknownEndpoint, "relative GraphQL endpoint routes to REST /api/v3/graphql, not GraphQL, on "+source.Host))
		}
	}
	input := &ast.Source{Name: "query.graphql", Input: data.query}
	if err := checkDepth(input); err != nil {
		return unknownResult(source, err)
	}
	document, err := parser.ParseQuery(input)
	if err != nil {
		return unknownResult(source, err)
	}
	selected, err := selectOperation(document, data.operationName)
	if err != nil {
		return unknownResult(source, err)
	}
	fragments := make(map[string]*ast.FragmentDefinition, len(document.Fragments))
	for _, f := range document.Fragments {
		if fragments[f.Name] != nil {
			return unknownResult(source, fail(model.UnknownMalformed, "duplicate fragment "+f.Name))
		}
		fragments[f.Name] = f
	}
	result := model.Result{
		SchemaVersion:  model.SchemaVersion,
		Source:         source,
		Classification: model.Classification{Level: model.LevelL3, Confidence: model.ConfidenceExact, Resolution: model.ResolutionComplete, MutationFree: selected.Operation == ast.Query || selected.Operation == ast.Mutation},
	}
	// Validate even unused fragments: cycles/missing spreads invalidate the document.
	check := selectionWalker{fragments: fragments, active: map[string]bool{}, resolver: &valueResolver{data: data, definition: selected}}
	for _, f := range document.Fragments {
		check.active[f.Name] = true
		if err := check.walk(f.SelectionSet, false, executionState{execution: model.ExecutionWillRun}, 0); err != nil {
			return unknownResult(source, err)
		}
		delete(check.active, f.Name)
	}
	for _, definition := range document.Operations {
		resolver := &valueResolver{data: data, definition: definition}
		state := executionState{execution: model.ExecutionWillRun, detail: data.note}
		if definition != selected {
			state.execution = model.ExecutionNotSelected
			state.evidence = "operation not selected by operationName"
		}
		state, err = state.directives(definition.Directives, "operation "+definition.Name, resolver)
		if err != nil {
			return unknownResult(source, err)
		}
		walker := selectionWalker{fragments: fragments, active: map[string]bool{}, resolver: resolver, opType: definition.Operation}
		if definition == selected && definition.Operation == ast.Mutation {
			walker.variableUses = make(map[string]int)
			walker.countDirectives(definition.Directives)
		}
		if err := walker.walk(definition.SelectionSet, true, state, 0); err != nil {
			return unknownResult(source, err)
		}
		if definition.Operation != ast.Mutation {
			if definition != selected {
				continue
			}
			op := model.Operation{Family: "graphql.query", Action: "graphql.query", Effect: model.EffectRead, Execution: model.ExecutionWillRun, Detail: data.note, Origin: model.Origin{Operation: definition.Name, OpType: string(definition.Operation)}}
			if definition.Operation == ast.Subscription {
				op.Family, op.Action, op.Effect, op.UnknownReason = model.FamilyUnknown, "graphql.subscription", model.EffectUnknown, model.UnknownMalformed
				op.Detail = joinText(op.Detail, "GitHub does not support subscriptions")
			}
			for _, root := range walker.roots {
				op.Detail = joinText(op.Detail, root.state.detail)
				op.Unresolved = append(op.Unresolved, root.state.unresolved...)
				if _, err := resolveArguments(root.field, resolver); err != nil {
					return unknownResult(source, err)
				}
				op.Unresolved = append(op.Unresolved, resolver.unresolved...)
			}
			setHost(&op, source.Host)
			result.Operations = append(result.Operations, op)
			continue
		}
		for _, root := range walker.roots {
			args, err := resolveArguments(root.field, resolver)
			if err != nil {
				return unknownResult(source, err)
			}
			input, _ := args["input"].(map[string]any)
			op := mutationOperation(root.field.Name, input)
			extractTexts(&op, root.field, input, req, resolver)
			extractEditable(&op, root.field, input, req, resolver, walker.variableUses)
			if input == nil {
				op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "parameters.input", Source: "variable", Reason: "mutation input unavailable or not an object"})
			}
			op.Execution, op.Evidence = root.state.execution, root.state.evidence
			op.Detail = joinText(op.Detail, root.state.detail)
			op.Origin = model.Origin{Field: root.field.Name, Operation: definition.Name, OpType: string(definition.Operation)}
			if root.field.Alias != root.field.Name {
				op.Origin.Alias = root.field.Alias
			}
			op.Unresolved = append(op.Unresolved, resolver.unresolved...)
			op.Unresolved = append(op.Unresolved, root.state.unresolved...)
			op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "target.repository", Source: "api", Reason: "repository requires node lookup"})
			setHost(&op, source.Host)
			if definition == selected && op.Execution != model.ExecutionSkipped {
				result.Classification.MutationFree = false
			}
			result.Operations = append(result.Operations, op)
		}
	}
	for _, op := range result.Operations {
		if len(op.Unresolved) > 0 {
			result.Classification.Resolution = model.ResolutionDeferred
		}
	}
	return result
}

func setHost(op *model.Operation, host string) {
	op.Target.Host = host
	if host == "" {
		op.Unresolved = append(op.Unresolved, model.Unresolved{Field: "target.host", Source: "api", Reason: "GitHub host not supplied"})
	}
}

func resolveArguments(field *ast.Field, resolver *valueResolver) (map[string]any, error) {
	resolver.unresolved = nil
	args := make(map[string]any, len(field.Arguments))
	for _, argument := range field.Arguments {
		if _, exists := args[argument.Name]; exists {
			return nil, fail(model.UnknownMalformed, "duplicate argument "+argument.Name)
		}
		value, err := resolver.value(argument.Value)
		if err != nil {
			return nil, err
		}
		args[argument.Name] = value
	}
	return args, nil
}

func selectOperation(doc *ast.QueryDocument, name string) (*ast.OperationDefinition, error) {
	names := make(map[string]bool, len(doc.Operations))
	var selected *ast.OperationDefinition
	for _, op := range doc.Operations {
		if names[op.Name] || (op.Name == "" && len(doc.Operations) > 1) {
			return nil, fail(model.UnknownMalformed, "duplicate or anonymous operation in multi-operation document")
		}
		names[op.Name] = true
		if name != "" && op.Name == name {
			selected = op
		}
	}
	if name != "" {
		if selected == nil {
			return nil, fail(model.UnknownMalformed, fmt.Sprintf("operationName %q not found", name))
		}
		return selected, nil
	}
	if len(doc.Operations) == 1 {
		return doc.Operations[0], nil
	}
	return nil, fail(model.UnknownOperationAmbig, "operationName required for multiple operations")
}
