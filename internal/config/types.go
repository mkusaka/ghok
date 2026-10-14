// Package config defines and loads ghok's YAML configuration.
// types.go is the shared contract; loading/validation/defaults live elsewhere
// in this package.
package config

import (
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is ~/.config/ghok/config.yaml (version 1).
type Config struct {
	Version int     `yaml:"version" json:"version"`
	Policy  Policy  `yaml:"policy" json:"policy"`
	Hooks   []Hook  `yaml:"hooks" json:"hooks"`
	Runtime Runtime `yaml:"runtime" json:"runtime"`
	Logging Logging `yaml:"logging" json:"logging"`
	Trust   Trust   `yaml:"trust" json:"trust"`
	// Dir is the directory the config was loaded from (relative hook commands
	// resolve against it). Not part of the YAML.
	Dir string `yaml:"-" json:"dir,omitempty"`
}

// Policy modes.
const (
	ModeObserve = "observe" // classify + log, never block
	ModeEnforce = "enforce" // deny according to rules / hooks
)

// Unknown handling values.
const (
	UnknownAllow = "allow"
	UnknownDeny  = "deny"
	UnknownAudit = "audit" // allow but record prominently
)

type Policy struct {
	Mode string `yaml:"mode" json:"mode"` // observe|enforce (default observe)
	// UnknownMutation applies to operations with Effect write/unknown that
	// could not be normalized (Family unknown) — anywhere. allow|deny|audit
	// (default audit).
	UnknownMutation string `yaml:"unknownMutation" json:"unknownMutation"`
	// ProtectedRepositories are "owner/repo" globs (path.Match). For these,
	// unknown/unresolved write operations are denied in enforce mode unless
	// ProtectedUnknown says otherwise (default deny = fail-closed).
	ProtectedRepositories []string `yaml:"protectedRepositories" json:"protectedRepositories"`
	ProtectedUnknown      string   `yaml:"protectedUnknown" json:"protectedUnknown"`
	// Rules are declarative deny/allow/audit rules evaluated without hooks.
	Rules []Rule `yaml:"rules" json:"rules"`
}

type Rule struct {
	ID       string `yaml:"id" json:"id"`
	Match    Match  `yaml:"match" json:"match"`
	Decision string `yaml:"decision" json:"decision"` // deny|audit (allow rules cannot override deny)
	Reason   string `yaml:"reason" json:"reason"`
}

// Match selects operations/invocations. All non-empty criteria must match
// (AND); entries within one list are OR. Globs use path.Match semantics with
// '.'-separated action names matched by MatchAction (see policy package).
type Match struct {
	Operations   StringList `yaml:"operation" json:"operation,omitempty"`       // action globs, e.g. "pr.merge.*"
	Commands     StringList `yaml:"command" json:"command,omitempty"`           // command path prefixes, e.g. "pr merge", "api"
	Repositories StringList `yaml:"repositories" json:"repositories,omitempty"` // "owner/repo" globs
	Hosts        StringList `yaml:"hosts" json:"hosts,omitempty"`
	Effects      StringList `yaml:"effect" json:"effect,omitempty"` // read|write|unknown
	Levels       StringList `yaml:"level" json:"level,omitempty"`   // L1|L2|L3|none
}

// Hook phases.
const (
	PhasePre  = "pre"
	PhasePost = "post"
)

// Hook onError values.
const (
	OnErrorDeny   = "deny"   // fail-closed (pre only)
	OnErrorAllow  = "allow"  // fail-open
	OnErrorIgnore = "ignore" // fail-open, no decision recorded
)

type Hook struct {
	ID       string            `yaml:"id" json:"id"`
	Phase    string            `yaml:"phase" json:"phase"` // pre|post
	Match    Match             `yaml:"match" json:"match"`
	Command  []string          `yaml:"command" json:"command"` // argv; relative paths resolve against Config.Dir
	Env      map[string]string `yaml:"env" json:"env,omitempty"`
	Timeout  Duration          `yaml:"timeout" json:"timeout"`   // default 10s
	OnError  string            `yaml:"onError" json:"onError"`   // pre default deny, post default ignore
	Priority int               `yaml:"priority" json:"priority"` // lower runs first; ties broken by config order
	// DependsOn lists hook IDs that must complete before this hook runs.
	DependsOn []string `yaml:"dependsOn" json:"dependsOn,omitempty"`
	// Optional: if the command is missing/not executable, skip instead of error.
	Optional bool  `yaml:"optional" json:"optional,omitempty"`
	Enabled  *bool `yaml:"enabled" json:"enabled,omitempty"` // default true
	// CaptureOutput (post only) streams gh stdout/stderr into the hook input,
	// bounded by Runtime.CaptureLimit. Default false.
	CaptureOutput bool `yaml:"captureOutput" json:"captureOutput,omitempty"`
	// MaxOutputBytes bounds what ghok reads from the hook's stdout/stderr
	// (default 1 MiB / 64 KiB).
	MaxOutputBytes int64 `yaml:"maxOutputBytes" json:"maxOutputBytes,omitempty"`
}

type Runtime struct {
	HookConcurrency int      `yaml:"hookConcurrency" json:"hookConcurrency"` // default 1 (sequential)
	StdinMaxBytes   int64    `yaml:"stdinMaxBytes" json:"stdinMaxBytes"`     // analysis spool limit, default 8 MiB
	StdinTimeout    Duration `yaml:"stdinTimeout" json:"stdinTimeout"`       // default 30s
	FileMaxBytes    int64    `yaml:"fileMaxBytes" json:"fileMaxBytes"`       // --input/@file read limit, default 8 MiB
	CaptureLimit    int64    `yaml:"captureLimit" json:"captureLimit"`       // post-hook stdout/stderr capture, default 1 MiB
	ResolveTimeout  Duration `yaml:"resolveTimeout" json:"resolveTimeout"`   // resolver API calls, default 5s
	// RealGh optionally pins the real gh path (otherwise discovered on PATH).
	RealGh string `yaml:"realGh" json:"realGh,omitempty"`
}

type Logging struct {
	Enabled *bool  `yaml:"enabled" json:"enabled,omitempty"` // default true
	Path    string `yaml:"path" json:"path,omitempty"`       // default <config dir>/logs/ghok.jsonl
	// Level: "info" logs every invocation; "decision" only non-allow decisions and errors.
	Level string `yaml:"level" json:"level,omitempty"`
}

// Trust controls project-level config. Project config (<repo>/.ghok.yaml) is
// ignored unless its absolute path is listed here; even then it can only add
// hooks/rules/protected repos and can never weaken global policy.
type Trust struct {
	Projects []string `yaml:"projects" json:"projects,omitempty"`
}

// StringList accepts either a YAML scalar or a sequence.
type StringList []string

func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = StringList{n.Value}
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*s = l
		return nil
	}
	return fmt.Errorf("line %d: expected string or list of strings", n.Line)
}

// Duration is a time.Duration that (un)marshals as "10s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q: %w", n.Line, s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

func (d Duration) D() time.Duration { return time.Duration(d) }
