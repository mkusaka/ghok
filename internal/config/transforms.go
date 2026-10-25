package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const DefaultTransformTimeout = 5 * time.Second

// Transform is a serial pre-execution request edit, not a policy hook.
type Transform struct {
	ID          string            `yaml:"id" json:"id" required:"true"`
	Match       Match             `yaml:"match" json:"match"`
	Unless      []Match           `yaml:"unless" json:"unless,omitempty" doc:"Operations matching any entry definitely (not only maybe) are excluded from match."`
	Command     []string          `yaml:"command" json:"command" required:"true"`
	Env         map[string]string `yaml:"env" json:"env,omitempty"`
	Timeout     Duration          `yaml:"timeout" json:"timeout"`
	OnError     string            `yaml:"onError" json:"onError" enum:"deny,allow"`
	RedactInput bool              `yaml:"redactInput" json:"redactInput,omitempty"`
	Files       []string          `yaml:"files,omitempty" json:"files,omitempty"`
	Source      string            `yaml:"-" json:"source,omitempty"`
}

type TransformList []Transform
type transformPhases struct {
	Pre []Transform `yaml:"pre,omitempty" json:"pre,omitempty"`
}

func (l TransformList) MarshalYAML() (any, error) {
	return transformPhases{Pre: []Transform(l)}, nil
}

func (l *TransformList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		*l = nil
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: transforms must be a mapping", n.Line)
	}
	if field(n, "post") != nil {
		return fmt.Errorf("transforms.post is reserved")
	}
	pre := field(n, "pre")
	if pre == nil {
		return nil
	}
	if pre.Kind == yaml.AliasNode {
		pre = pre.Alias
	}
	if pre.Kind == yaml.ScalarNode && pre.Tag == "!!null" {
		*l = nil
		return nil
	}
	if pre.Kind != yaml.SequenceNode {
		return fmt.Errorf("transforms.pre must be a list")
	}
	var out []Transform
	for i, item := range pre.Content {
		var t Transform
		if item.Kind == yaml.AliasNode {
			item = item.Alias
		}
		if item.Kind != yaml.MappingNode {
			return fmt.Errorf("transforms.pre[%d] must be a mapping", i)
		}
		if field(item, "continueOnError") != nil {
			return fmt.Errorf("transforms.pre[%d].continueOnError is not allowed; use onError: allow", i)
		}
		for _, key := range []string{"parallel", "background", "wait", "wait-all", "dependsOn", "optional", "captureOutput", "enabled", "maxOutputBytes", "phase"} {
			if field(item, key) != nil {
				return fmt.Errorf("transforms.pre[%d].%s is not allowed", i, key)
			}
		}
		if err := item.Decode(&t); err != nil {
			return err
		}
		if field(item, "timeout") == nil {
			t.Timeout = Duration(DefaultTransformTimeout)
		}
		if field(item, "onError") == nil {
			t.OnError = OnErrorDeny
		}
		out = append(out, t)
	}
	*l = out
	return nil
}

func (t Transform) hook() Hook {
	return Hook{ID: t.ID, Phase: "transform", Match: t.Match, Unless: t.Unless, Command: t.Command, Env: t.Env, Timeout: t.Timeout, OnError: t.OnError, RedactInput: t.RedactInput, Files: t.Files, Source: t.Source}
}

// trustHooks shares the per-command content trust machinery without making
// transforms executable hooks or counting them as active policy gates.
func (c *Config) trustHooks() []Hook {
	all := make([]Hook, 0, len(c.Hooks)+len(c.Transforms)+len(c.Delegates))
	all = append(all, c.Hooks...)
	for _, t := range c.Transforms {
		all = append(all, t.hook())
	}
	for _, d := range c.Delegates {
		all = append(all, d.hook())
	}
	return all
}

func (c *Config) TransformCommandPath(t Transform) string {
	if len(t.Command) == 0 {
		return ""
	}
	p := t.Command[0]
	if !filepath.IsAbs(p) && strings.Contains(p, "/") {
		return filepath.Join(c.Dir, p)
	}
	return p
}
