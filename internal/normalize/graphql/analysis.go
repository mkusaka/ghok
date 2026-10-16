package graphql

import (
	"fmt"
	"strconv"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/lexer"
)

// Tokenization avoids counting brackets in strings, block strings and comments.
// Bound every recursive grammar production before entering the parser.
func checkDepth(source *ast.Source) error {
	l := lexer.New(source)
	depth := 0
	for {
		t, err := l.ReadToken()
		if err != nil {
			return err
		}
		switch t.Kind {
		case lexer.EOF:
			return nil
		case lexer.BraceL, lexer.BracketL, lexer.ParenL:
			depth++
			if depth > maxDepth {
				return fail(model.UnknownInputTooLarge, "GraphQL nesting exceeds 256")
			}
		case lexer.BraceR, lexer.BracketR, lexer.ParenR:
			depth--
		}
	}
}

type valueResolver struct {
	data       requestData
	definition *ast.OperationDefinition
	unresolved []model.Unresolved
}

func (r *valueResolver) missing(name string) {
	field := "variables." + name
	for _, u := range r.unresolved {
		if u.Field == field {
			return
		}
	}
	u := model.Unresolved{Field: field, Source: "variable", Reason: "variable value unavailable"}
	if known, ok := r.data.unavailable[name]; ok {
		u.Reason = known.Reason
	}
	r.unresolved = append(r.unresolved, u)
}
func (r *valueResolver) value(v *ast.Value) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch v.Kind {
	case ast.Variable:
		if _, blocked := r.data.unavailable[v.Raw]; blocked {
			r.missing(v.Raw)
			// Nested field reads can leave a partially known input object.
			return r.data.variables[v.Raw], nil
		}
		if x, ok := r.data.variables[v.Raw]; ok {
			return x, nil
		}
		for _, def := range r.definition.VariableDefinitions {
			if def.Variable == v.Raw && def.DefaultValue != nil {
				return r.value(def.DefaultValue)
			}
		}
		r.missing(v.Raw)
		return nil, nil
	case ast.IntValue:
		return strconv.ParseInt(v.Raw, 10, 64)
	case ast.FloatValue:
		return strconv.ParseFloat(v.Raw, 64)
	case ast.BooleanValue:
		return strconv.ParseBool(v.Raw)
	case ast.StringValue, ast.BlockValue, ast.EnumValue:
		return v.Raw, nil
	case ast.NullValue:
		return nil, nil
	case ast.ObjectValue:
		m := make(map[string]any, len(v.Children))
		for _, c := range v.Children {
			if _, exists := m[c.Name]; exists {
				return nil, fail(model.UnknownMalformed, "duplicate input field "+c.Name)
			}
			x, err := r.value(c.Value)
			if err != nil {
				return nil, err
			}
			m[c.Name] = x
		}
		return m, nil
	case ast.ListValue:
		list := make([]any, 0, len(v.Children))
		for _, c := range v.Children {
			x, err := r.value(c.Value)
			if err != nil {
				return nil, err
			}
			list = append(list, x)
		}
		return list, nil
	}
	return nil, fail(model.UnknownMalformed, "unsupported AST value")
}

type executionState struct {
	execution        model.Execution
	evidence, detail string
	unresolved       []model.Unresolved
}

func joinText(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}
func (s executionState) directives(directives ast.DirectiveList, label string, r *valueResolver) (executionState, error) {
	for _, d := range directives {
		if d.Name != "skip" && d.Name != "include" {
			s.detail = joinText(s.detail, "unknown directive @"+d.Name+" ignored for execution")
			continue
		}
		if len(d.Arguments) != 1 || d.Arguments[0].Name != "if" {
			return s, fail(model.UnknownMalformed, "@"+d.Name+" requires if")
		}
		r.unresolved = nil
		x, err := r.value(d.Arguments[0].Value)
		if err != nil {
			return s, err
		}
		s.unresolved = append(s.unresolved, r.unresolved...)
		b, known := x.(bool)
		if !known {
			if s.execution == model.ExecutionWillRun {
				s.execution = model.ExecutionMaybe
			}
			s.evidence = joinText(s.evidence, "@"+d.Name+" condition unresolved on "+label)
		} else if (d.Name == "skip" && b) || (d.Name == "include" && !b) {
			if s.execution != model.ExecutionNotSelected {
				s.execution = model.ExecutionSkipped
			}
			s.evidence = joinText(s.evidence, fmt.Sprintf("@%s(if: %t) on %s", d.Name, b, label))
		}
	}
	return s, nil
}

type rootField struct {
	field *ast.Field
	state executionState
}
type selectionWalker struct {
	fragments    map[string]*ast.FragmentDefinition
	active       map[string]bool
	visits       int
	roots        []rootField
	resolver     *valueResolver
	opType       ast.Operation
	variableUses map[string]int
}

func (w *selectionWalker) walk(set ast.SelectionSet, root bool, state executionState, depth int) error {
	if depth > maxDepth {
		return fail(model.UnknownInputTooLarge, "fragment expansion nesting exceeds 256")
	}
	for _, selection := range set {
		w.visits++
		if w.visits > maxSelections {
			return fail(model.UnknownInputTooLarge, "expanded selections exceed 10000")
		}
		switch s := selection.(type) {
		case *ast.Field:
			w.countArguments(s.Arguments)
			w.countDirectives(s.Directives)
			label := "field " + s.Name
			if s.Alias != "" && s.Alias != s.Name {
				label = "alias " + s.Alias
			}
			next, err := state.directives(s.Directives, label, w.resolver)
			if err != nil {
				return err
			}
			if root {
				w.roots = append(w.roots, rootField{s, next})
			}
			if err := w.walk(s.SelectionSet, false, next, depth+1); err != nil {
				return err
			}
		case *ast.InlineFragment:
			w.countDirectives(s.Directives)
			if root && s.TypeCondition != "" && s.TypeCondition != rootType(w.opType) {
				return fail(model.UnknownMalformed, "incompatible root fragment type "+s.TypeCondition)
			}
			next, err := state.directives(s.Directives, "inline fragment", w.resolver)
			if err != nil {
				return err
			}
			if err := w.walk(s.SelectionSet, root, next, depth+1); err != nil {
				return err
			}
		case *ast.FragmentSpread:
			f := w.fragments[s.Name]
			if f == nil {
				return fail(model.UnknownMalformed, "undefined fragment "+s.Name)
			}
			w.countDirectives(s.Directives)
			w.countDirectives(f.Directives)
			if w.active[s.Name] {
				return fail(model.UnknownMalformed, "fragment cycle at "+s.Name)
			}
			if root && f.TypeCondition != rootType(w.opType) {
				return fail(model.UnknownMalformed, "incompatible root fragment type "+f.TypeCondition)
			}
			next, err := state.directives(s.Directives, "fragment spread "+s.Name, w.resolver)
			if err != nil {
				return err
			}
			next, err = next.directives(f.Directives, "fragment "+s.Name, w.resolver)
			if err != nil {
				return err
			}
			w.active[s.Name] = true
			err = w.walk(f.SelectionSet, root, next, depth+1)
			delete(w.active, s.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Count every expanded use, not only input values: changing a variable that
// also controls a directive or another argument would change an unedited view.
func (w *selectionWalker) countArguments(arguments ast.ArgumentList) {
	if w.variableUses == nil {
		return
	}
	for _, argument := range arguments {
		w.countValue(argument.Value)
	}
}

func (w *selectionWalker) countDirectives(directives ast.DirectiveList) {
	if w.variableUses == nil {
		return
	}
	for _, directive := range directives {
		w.countArguments(directive.Arguments)
	}
}

func (w *selectionWalker) countValue(value *ast.Value) {
	if value == nil {
		return
	}
	if value.Kind == ast.Variable {
		w.variableUses[value.Raw]++
	}
	for _, child := range value.Children {
		w.countValue(child.Value)
	}
}
func rootType(op ast.Operation) string {
	switch op {
	case ast.Mutation:
		return "Mutation"
	case ast.Subscription:
		return "Subscription"
	default:
		return "Query"
	}
}
