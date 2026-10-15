package config

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// HookList holds the hooks of both phases, written as one list per phase:
//
//	hooks:
//	  pre: [...]
//	  post: [...]
//
// Besides hooks, each list accepts GitHub Actions style control items,
// flattened at parse time:
//
//   - parallel: [hook, ...]  # members run as background hooks, joined at the end
//   - wait: id | [id, ...]   # join earlier background hooks
//   - wait-all:              # join every earlier background hook
//
// The flat list holds pre hooks, then post hooks, with Phase set from the
// list. A hook starts after every earlier non-background hook of its phase
// and its Wait list.
type HookList []Hook

// hookPhases is the YAML shape of HookList; hookItems marks a phase list for
// the unknown-key walker.
type hookPhases struct {
	Pre  hookItems `yaml:"pre,omitempty"`
	Post hookItems `yaml:"post,omitempty"`
}

type hookItems []Hook

func (l HookList) MarshalYAML() (any, error) {
	var phases hookPhases
	for _, h := range l {
		if h.Phase == PhasePost {
			phases.Post = append(phases.Post, h)
		} else {
			phases.Pre = append(phases.Pre, h)
		}
	}
	return phases, nil
}

func (l *HookList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		*l = nil
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: hooks must be a mapping with pre:/post: lists; move each hook under its phase and remove its phase key", n.Line)
	}
	var hooks HookList
	for _, phase := range []string{PhasePre, PhasePost} {
		list := field(n, phase)
		if list == nil {
			continue
		}
		parsed, err := parsePhase(list, phase)
		if err != nil {
			return err
		}
		hooks = append(hooks, parsed...)
	}
	*l = hooks
	return nil
}

func parsePhase(n *yaml.Node, phase string) (HookList, error) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("line %d: hooks.%s must be a list", n.Line, phase)
	}
	var hooks HookList
	var waits, background []string
	decode := func(item *yaml.Node, path string) (Hook, error) {
		var h Hook
		if v := field(item, "phase"); v != nil {
			return h, fmt.Errorf("line %d: %s.phase: phase is set by the hooks.pre/hooks.post list", v.Line, path)
		}
		if err := item.Decode(&h); err != nil {
			return h, fmt.Errorf("%s: %w", path, err)
		}
		h.Phase = phase
		defaultHook(&h, item)
		h.Wait = slices.Clone(waits)
		return h, nil
	}
	for i, item := range n.Content {
		path := fmt.Sprintf("hooks.%s[%d]", phase, i)
		if item.Kind == yaml.AliasNode {
			item = item.Alias
		}
		switch control := controlKey(item); control {
		case "":
			h, err := decode(item, path)
			if err != nil {
				return nil, err
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
				return nil, fmt.Errorf("line %d: %s.parallel: must be a non-empty list of hooks", item.Line, path)
			}
			var ids []string
			for j, member := range members.Content {
				p := fmt.Sprintf("%s.parallel[%d]", path, j)
				if member.Kind == yaml.AliasNode {
					member = member.Alias
				}
				if c := controlKey(member); c != "" {
					return nil, fmt.Errorf("line %d: %s: %s is not allowed inside parallel", member.Line, p, c)
				}
				h, err := decode(member, p)
				if err != nil {
					return nil, err
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
				return nil, fmt.Errorf("line %d: %s.wait: must be a hook ID or a non-empty list of IDs", item.Line, path)
			}
			for _, id := range ids {
				if !slices.Contains(background, id) {
					return nil, fmt.Errorf("line %d: %s.wait: %s is not an earlier background hook in this phase list", item.Line, path, id)
				}
			}
			waits = join(waits, ids)
		case "wait-all":
			if v := field(item, "wait-all"); v.Tag != "!!null" {
				return nil, fmt.Errorf("line %d: %s.wait-all: takes no value", item.Line, path)
			}
			waits = join(waits, background)
		default:
			return nil, fmt.Errorf("line %d: %s: %s", item.Line, path, control)
		}
	}
	return hooks, nil
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

// backgroundIDs lists a phase's background hooks, which later config files join.
func (l HookList) backgroundIDs(phase string) []string {
	var ids []string
	for _, h := range l {
		if h.Background && h.Phase == phase {
			ids = append(ids, h.ID)
		}
	}
	return ids
}
