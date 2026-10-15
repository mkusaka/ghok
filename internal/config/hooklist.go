package config

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// HookList is the hooks sequence. Besides hooks it accepts GitHub Actions
// style control items, flattened at parse time:
//
//   - parallel: [hook, ...]  # members run as background hooks, joined at the end
//   - wait: id | [id, ...]   # join earlier background hooks
//   - wait-all:              # join every earlier background hook
//
// A hook starts after every earlier non-background hook and its Wait list.
type HookList []Hook

func (l *HookList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		*l = nil
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: hooks must be a list", n.Line)
	}
	var hooks HookList
	var waits, background []string
	decode := func(item *yaml.Node, path string) (Hook, error) {
		var h Hook
		if err := item.Decode(&h); err != nil {
			return h, fmt.Errorf("%s: %w", path, err)
		}
		defaultHook(&h, item)
		h.Wait = slices.Clone(waits)
		return h, nil
	}
	for i, item := range n.Content {
		path := fmt.Sprintf("hooks[%d]", i)
		if item.Kind == yaml.AliasNode {
			item = item.Alias
		}
		switch control := controlKey(item); control {
		case "":
			h, err := decode(item, path)
			if err != nil {
				return err
			}
			if h.Background {
				background = append(background, h.ID)
			}
			hooks = append(hooks, h)
		case "parallel":
			members := field(item, "parallel")
			if members.Kind == yaml.AliasNode {
				members = members.Alias
			}
			if members.Kind != yaml.SequenceNode || len(members.Content) == 0 {
				return fmt.Errorf("line %d: %s.parallel: must be a non-empty list of hooks", item.Line, path)
			}
			var ids []string
			for j, member := range members.Content {
				p := fmt.Sprintf("%s.parallel[%d]", path, j)
				if member.Kind == yaml.AliasNode {
					member = member.Alias
				}
				if c := controlKey(member); c != "" {
					return fmt.Errorf("line %d: %s: %s is not allowed inside parallel", member.Line, p, c)
				}
				h, err := decode(member, p)
				if err != nil {
					return err
				}
				h.Background = true
				ids = append(ids, h.ID)
				hooks = append(hooks, h)
			}
			background = append(background, ids...)
			waits = join(waits, ids)
		case "wait":
			var ids StringList
			if err := field(item, "wait").Decode(&ids); err != nil || len(ids) == 0 {
				return fmt.Errorf("line %d: %s.wait: must be a hook ID or a non-empty list of IDs", item.Line, path)
			}
			for _, id := range ids {
				if !slices.Contains(background, id) {
					return fmt.Errorf("line %d: %s.wait: %s is not an earlier background hook", item.Line, path, id)
				}
			}
			waits = join(waits, ids)
		case "wait-all":
			if v := field(item, "wait-all"); v.Tag != "!!null" {
				return fmt.Errorf("line %d: %s.wait-all: takes no value", item.Line, path)
			}
			waits = join(waits, background)
		default:
			return fmt.Errorf("line %d: %s: %s", item.Line, path, control)
		}
	}
	*l = hooks
	return nil
}

// controlKey names the control item kind, "" for a hook, or an error message
// when a control key is mixed with other keys.
func controlKey(item *yaml.Node) string {
	if item.Kind != yaml.MappingNode {
		return ""
	}
	for _, key := range []string{"parallel", "wait", "wait-all"} {
		if field(item, key) != nil {
			if len(item.Content) != 2 {
				return key + " must be the only key of its item"
			}
			return key
		}
	}
	return ""
}

func join(waits, ids []string) []string {
	for _, id := range ids {
		if !slices.Contains(waits, id) {
			waits = append(waits, id)
		}
	}
	return waits
}

// backgroundIDs lists the background hooks, which later config files join.
func (l HookList) backgroundIDs() []string {
	var ids []string
	for _, h := range l {
		if h.Background {
			ids = append(ids, h.ID)
		}
	}
	return ids
}
