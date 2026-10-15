package config

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var hookID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var matchEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate aggregates independent failures with paths suitable for diagnostics.
func (c *Config) Validate() error {
	var errs []error
	add := func(p, message string) { errs = append(errs, fmt.Errorf("%s: %s", p, message)) }
	enum := func(p, v string, choices ...string) {
		for _, choice := range choices {
			if v == choice {
				return
			}
		}
		add(p, "must be one of "+strings.Join(choices, ", "))
	}
	globs := func(p string, patterns []string, repository bool) {
		for i, pattern := range patterns {
			key := fmt.Sprintf("%s[%d]", p, i)
			if _, err := path.Match(pattern, ""); err != nil {
				add(key, "invalid glob: "+err.Error())
			}
			if repository {
				owner, name, ok := strings.Cut(pattern, "/")
				if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
					add(key, "must have owner/name shape")
				}
			}
		}
	}
	match := func(p string, m Match) {
		globs(p+".operation", m.Operations, false)
		globs(p+".command", m.Commands, false)
		globs(p+".repositories", m.Repositories, true)
		globs(p+".hosts", m.Hosts, false)
		for name, pattern := range m.Env {
			key := fmt.Sprintf("%s.env[%q]", p, name)
			if !matchEnvName.MatchString(name) {
				add(key, "invalid environment variable name: must match [A-Za-z_][A-Za-z0-9_]*")
			}
			if _, err := path.Match(pattern, ""); err != nil {
				add(key, "invalid glob: "+err.Error())
			}
		}
		for i, v := range m.Effects {
			enum(fmt.Sprintf("%s.effect[%d]", p, i), v, "read", "write", "unknown")
		}
		for i, v := range m.Levels {
			enum(fmt.Sprintf("%s.level[%d]", p, i), v, "L1", "L2", "L3", "none")
		}
		for i, v := range m.Tokens {
			enum(fmt.Sprintf("%s.token[%d]", p, i), v, "app", "user", "unknown", "none")
		}
		for i, v := range m.Results {
			enum(fmt.Sprintf("%s.result[%d]", p, i), v, ResultSuccess, ResultFailure)
		}
		for i, v := range m.ExitCodes {
			if v < 0 || v > 255 {
				add(fmt.Sprintf("%s.exitCode[%d]", p, i), "must be 0-255")
			}
		}
	}
	// gh has not run yet for rules and pre hooks, so outcome filters cannot apply.
	outcomeOnlyPost := func(p string, m Match) {
		if len(m.Results) > 0 {
			add(p+".result", "allowed only for post hooks")
		}
		if len(m.ExitCodes) > 0 {
			add(p+".exitCode", "allowed only for post hooks")
		}
	}
	if c.Version != DefaultVersion {
		add("version", "must be 1")
	}
	enum("policy.mode", c.Policy.Mode, ModeObserve, ModeEnforce)
	enum("policy.unknownMutation", c.Policy.UnknownMutation, UnknownAllow, UnknownAudit, UnknownDeny)
	enum("policy.protectedUnknown", c.Policy.ProtectedUnknown, UnknownAllow, UnknownAudit, UnknownDeny)
	globs("policy.protectedRepositories", c.Policy.ProtectedRepositories, true)
	rules := map[string]bool{}
	for i, r := range c.Policy.Rules {
		p := fmt.Sprintf("policy.rules[%d]", i)
		if strings.TrimSpace(r.ID) == "" {
			add(p+".id", "must not be empty")
		}
		if rules[r.ID] {
			add(p+".id", "duplicate rule ID "+r.ID)
		}
		rules[r.ID] = true
		enum(p+".decision", r.Decision, "deny", "audit")
		match(p+".match", r.Match)
		outcomeOnlyPost(p+".match", r.Match)
	}
	hooks := map[string]int{}
	for i, h := range c.Hooks {
		p := fmt.Sprintf("hooks[%d]", i)
		if !hookID.MatchString(h.ID) {
			add(p+".id", "must be non-empty and match ^[A-Za-z0-9._-]+$")
		}
		if _, exists := hooks[h.ID]; exists {
			add(p+".id", "duplicate hook ID "+h.ID)
		} else {
			hooks[h.ID] = i
		}
		enum(p+".phase", h.Phase, PhasePre, PhasePost)
		if len(h.Command) == 0 || strings.TrimSpace(h.Command[0]) == "" {
			add(p+".command", "executable must not be empty")
		}
		enum(p+".onError", h.OnError, OnErrorDeny, OnErrorAllow, OnErrorIgnore)
		if h.Phase == PhasePost && h.OnError == OnErrorDeny {
			add(p+".onError", "post hooks cannot deny")
		}
		if h.Timeout <= 0 || h.Timeout.D() > MaxHookTimeout {
			add(p+".timeout", "must be > 0 and <= "+MaxHookTimeout.String())
		}
		if h.MaxOutputBytes <= 0 {
			add(p+".maxOutputBytes", "must be positive")
		}
		if h.CaptureOutput && h.Phase != PhasePost {
			add(p+".captureOutput", "allowed only for post hooks")
		}
		match(p+".match", h.Match)
		if h.Phase != PhasePost {
			outcomeOnlyPost(p+".match", h.Match)
		}
	}
	edges := make([][]int, len(c.Hooks))
	for i, h := range c.Hooks {
		for j, dep := range h.DependsOn {
			p := fmt.Sprintf("hooks[%d].dependsOn[%d]", i, j)
			target, exists := hooks[dep]
			switch {
			case !exists:
				add(p, "unknown hook "+dep)
			case target == i:
				add(p, "self dependency")
			case c.Hooks[target].Phase != h.Phase:
				add(p, "dependency must have the same phase")
			default:
				edges[i] = append(edges[i], target)
			}
		}
	}
	// Iterative DFS avoids stack growth on large user-supplied dependency graphs.
	colors := make([]uint8, len(c.Hooks))
	type frame struct{ node, edge int }
	for root := range c.Hooks {
		if colors[root] != 0 {
			continue
		}
		stack := []frame{{node: root}}
		colors[root] = 1
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.edge == len(edges[top.node]) {
				colors[top.node] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			dep := edges[top.node][top.edge]
			top.edge++
			if colors[dep] == 1 {
				add(fmt.Sprintf("hooks[%d].dependsOn", top.node), "dependency cycle involving "+c.Hooks[dep].ID)
			}
			if colors[dep] == 0 {
				colors[dep] = 1
				stack = append(stack, frame{node: dep})
			}
		}
	}
	for _, limit := range []struct {
		p string
		n int64
	}{
		{"runtime.hookConcurrency", int64(c.Runtime.HookConcurrency)},
		{"runtime.stdinMaxBytes", c.Runtime.StdinMaxBytes},
		{"runtime.stdinTimeout", int64(c.Runtime.StdinTimeout)},
		{"runtime.fileMaxBytes", c.Runtime.FileMaxBytes},
		{"runtime.captureLimit", c.Runtime.CaptureLimit},
		{"runtime.resolveTimeout", int64(c.Runtime.ResolveTimeout)},
	} {
		if limit.n <= 0 {
			add(limit.p, "must be positive")
		}
	}
	enum("logging.level", c.Logging.Level, "info", "decision")
	return errors.Join(errs...)
}
