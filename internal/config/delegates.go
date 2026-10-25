package config

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	DefaultDelegateTimeout = 10 * time.Second
	OnErrorFallback        = "fallback"
)

// Delegate replaces only the final execution step, never policy or hooks.
type Delegate struct {
	ID      string            `yaml:"id" json:"id" required:"true"`
	Match   Match             `yaml:"match" json:"match" required:"true" doc:"Must explicitly select effect: read and level: L1 (native) or L2 (REST). L2 also requires method: GET."`
	Unless  []Match           `yaml:"unless" json:"unless,omitempty"`
	Command []string          `yaml:"command" json:"command" required:"true" doc:"Executable and fixed arguments; gh argv is appended unchanged. No shell. Relative executable paths resolve against the config directory."`
	Env     map[string]string `yaml:"env" json:"env,omitempty"`
	Timeout Duration          `yaml:"timeout" json:"timeout" doc:"Positive Go duration, default 10s, maximum 10m."`
	OnError string            `yaml:"onError" json:"onError" enum:"fallback,deny" doc:"Start failure or timeout before output: fallback (default) to real gh, or deny with exit 78. Never retry after output or a signal."`
	Files   []string          `yaml:"files,omitempty" json:"files,omitempty" doc:"Project only: repository-relative dependency files included in this delegate's trust digest."`
	Source  string            `yaml:"-" json:"source,omitempty"`
}

func (d *Delegate) UnmarshalYAML(n *yaml.Node) error {
	type plain Delegate
	value := plain{Timeout: Duration(DefaultDelegateTimeout), OnError: OnErrorFallback}
	if err := n.Decode(&value); err != nil {
		return err
	}
	*d = Delegate(value)
	return nil
}

func (d Delegate) hook() Hook {
	return Hook{ID: d.ID, Phase: "delegate", Match: d.Match, Unless: d.Unless, Command: d.Command, Env: d.Env, Timeout: d.Timeout, OnError: d.OnError, Files: d.Files, Source: d.Source}
}

func (c *Config) DelegateCommandPath(d Delegate) string { return c.HookCommandPath(d.hook()) }

// Require explicit, conjunctive safety constraints rather than guessing from
// action names or trusting an unless blacklist. Runtime selection additionally
// checks the entire normalized invocation, including unknown flags/operations.
func validateDelegateReadOnly(m Match) error {
	if !slices.Equal([]string(m.Effects), []string{"read"}) {
		return fmt.Errorf("must explicitly set effect: read; delegates cannot include mutations")
	}
	if len(m.Levels) == 0 {
		return fmt.Errorf("must explicitly select level: L1 or L2; GraphQL is not delegatable")
	}
	for _, level := range m.Levels {
		if level != "L1" && level != "L2" {
			return fmt.Errorf("only level L1 and L2 are delegatable; GraphQL is not delegatable")
		}
	}
	if slices.Contains(m.Levels, "L2") && !slices.Equal([]string(m.Methods), []string{"GET"}) {
		return fmt.Errorf("REST delegates must explicitly set method: GET; non-GET API requests are not delegatable")
	}
	return nil
}

func validateDelegateCommand(d Delegate) error {
	for _, arg := range d.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command must not contain NUL")
		}
	}
	for name, value := range d.Env {
		if !matchEnvName.MatchString(name) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("env must contain valid names and NUL-free values")
		}
	}
	return nil
}
