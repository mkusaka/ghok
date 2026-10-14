package redact

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"
)

func looksGraphQL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "#") {
		return true
	}
	for _, keyword := range []string{"query", "mutation", "subscription", "fragment"} {
		if strings.HasPrefix(s, keyword) && len(s) > len(keyword) {
			c := s[len(keyword)]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '(' || c == '{' {
				return true
			}
		}
	}
	return false
}

// graphQL parses before pattern replacement: redacting the source first would
// obscure syntax or sensitive argument names that also happen to be secrets.
// Invalid/truncated documents are withheld rather than leaking unparsed values.
func (r *Redactor) graphQL(s string) string {
	if s == "" {
		return ""
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: s})
	if err != nil {
		return r.mark()
	}
	for _, op := range doc.Operations {
		r.graphQLVariables(op.VariableDefinitions)
		r.graphQLDirectives(op.Directives)
		r.graphQLSelections(op.SelectionSet)
	}
	for _, fragment := range doc.Fragments {
		r.graphQLVariables(fragment.VariableDefinition)
		r.graphQLDirectives(fragment.Directives)
		r.graphQLSelections(fragment.SelectionSet)
	}
	var out strings.Builder
	formatter.NewFormatter(&out, formatter.WithCompacted()).FormatQueryDocument(doc)
	return r.String(out.String())
}

func (r *Redactor) graphQLVariables(vars ast.VariableDefinitionList) {
	for _, variable := range vars {
		r.graphQLValue(variable.DefaultValue, sensitiveName(variable.Variable))
		r.graphQLDirectives(variable.Directives)
	}
}

func (r *Redactor) graphQLSelections(selections ast.SelectionSet) {
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.Field:
			r.graphQLArguments(selection.Arguments)
			r.graphQLDirectives(selection.Directives)
			r.graphQLSelections(selection.SelectionSet)
		case *ast.InlineFragment:
			r.graphQLDirectives(selection.Directives)
			r.graphQLSelections(selection.SelectionSet)
		case *ast.FragmentSpread:
			r.graphQLDirectives(selection.Directives)
		}
	}
}

func (r *Redactor) graphQLDirectives(directives ast.DirectiveList) {
	for _, directive := range directives {
		r.graphQLArguments(directive.Arguments)
	}
}

func (r *Redactor) graphQLArguments(arguments ast.ArgumentList) {
	for _, argument := range arguments {
		r.graphQLValue(argument.Value, sensitiveName(argument.Name))
	}
}

func (r *Redactor) graphQLValue(value *ast.Value, sensitive bool) {
	if value == nil {
		return
	}
	if value.Kind == ast.StringValue || value.Kind == ast.BlockValue {
		if sensitive {
			value.Raw = r.mark()
		} else {
			value.Raw = r.String(value.Raw)
		}
		// Normal quoted strings give predictable escaping, including when a
		// private-key block originally occupied a GraphQL block string.
		value.Kind = ast.StringValue
	}
	for _, child := range value.Children {
		r.graphQLValue(child.Value, sensitive || sensitiveName(child.Name))
	}
}
