package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	DefaultVersion                   = 1
	DefaultMode                      = ModeObserve
	DefaultUnknownMutation           = UnknownAudit
	DefaultUnknownInput              = UnknownAudit
	DefaultProtectedUnknown          = UnknownDeny
	DefaultHookTimeout               = 10 * time.Second
	MaxHookTimeout                   = 10 * time.Minute
	DefaultPreOnError                = OnErrorDeny
	DefaultPostOnError               = OnErrorIgnore
	DefaultEnabled                   = true
	DefaultHookConcurrency           = 1
	DefaultStdinMaxBytes       int64 = 8 << 20
	DefaultStdinTimeout              = 3 * time.Second
	DefaultFileMaxBytes        int64 = 8 << 20
	DefaultCaptureLimit        int64 = 1 << 20
	DefaultResolveTimeout            = 2 * time.Second
	DefaultHookMaxOutputBytes  int64 = 1 << 20
	DefaultLoggingLevel              = "info"
	DefaultLoggingRelativePath       = "logs/ghok.jsonl"
)

// Dir uses only the supplied environment, so callers can isolate config lookup.
func Dir(env map[string]string) string {
	if d := env["GHOK_CONFIG_DIR"]; d != "" {
		return d
	}
	if d := env["XDG_CONFIG_HOME"]; d != "" {
		return filepath.Join(d, "ghok")
	}
	return filepath.Join(env["HOME"], ".config", "ghok")
}
func DefaultPath(env map[string]string) string { return filepath.Join(Dir(env), "config.yaml") }

func (c *Config) ApplyDefaults() {
	if c.Version == 0 {
		c.Version = DefaultVersion
	}
	if c.Policy.Mode == "" {
		c.Policy.Mode = DefaultMode
	}
	if c.Policy.UnknownMutation == "" {
		c.Policy.UnknownMutation = DefaultUnknownMutation
	}
	if c.Policy.UnknownInput == "" {
		c.Policy.UnknownInput = DefaultUnknownInput
	}
	if c.Policy.ProtectedUnknown == "" {
		c.Policy.ProtectedUnknown = DefaultProtectedUnknown
	}
	if c.Runtime.HookConcurrency == 0 {
		c.Runtime.HookConcurrency = DefaultHookConcurrency
	}
	if c.Runtime.StdinMaxBytes == 0 {
		c.Runtime.StdinMaxBytes = DefaultStdinMaxBytes
	}
	if c.Runtime.StdinTimeout == 0 {
		c.Runtime.StdinTimeout = Duration(DefaultStdinTimeout)
	}
	if c.Runtime.FileMaxBytes == 0 {
		c.Runtime.FileMaxBytes = DefaultFileMaxBytes
	}
	if c.Runtime.CaptureLimit == 0 {
		c.Runtime.CaptureLimit = DefaultCaptureLimit
	}
	if c.Runtime.ResolveTimeout == 0 {
		c.Runtime.ResolveTimeout = Duration(DefaultResolveTimeout)
	}
	if c.Runtime.ResolveAPI == "" {
		c.Runtime.ResolveAPI = "auto"
	}
	if c.Logging.Enabled == nil {
		b := DefaultEnabled
		c.Logging.Enabled = &b
	}
	if c.Logging.Path == "" {
		c.Logging.Path = filepath.Join(c.Dir, DefaultLoggingRelativePath)
	}
	if c.Logging.Level == "" {
		c.Logging.Level = DefaultLoggingLevel
	}
	for i := range c.Hooks {
		defaultHook(&c.Hooks[i], nil)
	}
}

func defaultHook(h *Hook, n *yaml.Node) {
	if h.Timeout == 0 && field(n, "timeout") == nil {
		h.Timeout = Duration(DefaultHookTimeout)
	}
	if h.OnError == "" && field(n, "onError") == nil {
		h.OnError = DefaultPreOnError
		if h.Phase == PhasePost {
			h.OnError = DefaultPostOnError
		}
	}
	if h.Enabled == nil {
		b := DefaultEnabled
		h.Enabled = &b
	}
	if h.MaxOutputBytes == 0 && field(n, "maxOutputBytes") == nil {
		h.MaxOutputBytes = DefaultHookMaxOutputBytes
	}
}

// userConfigError retains the raw policy mode even when strict decoding fails.
type userConfigError struct {
	error
	enforce bool
}

func (e *userConfigError) Unwrap() error { return e.error }

// FailClosed reports whether an invalid user config must prevent gh execution.
func FailClosed(err error, env map[string]string) bool {
	var failure *userConfigError
	return err != nil && (env["GHOK_FAIL_CLOSED"] == "1" || errors.As(err, &failure) && failure.enforce)
}

func invalidUserConfig(path string, data []byte, cause error) error {
	var mode string
	if n, err := document(data); err == nil {
		if value := field(field(n, "policy"), "mode"); value != nil {
			_ = value.Decode(&mode)
		}
	}
	return &userConfigError{error: fmt.Errorf("config %s: %w", path, cause), enforce: mode == ModeEnforce}
}

// Load treats an absent file as an empty configuration, not as an error.
func Load(path string) (*Config, error) {
	return load(path, nil)
}

// load records source status from the same read used for parsing.
func load(path string, notes *[]string) (*Config, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(absolute)
	if notes != nil {
		status := "exists"
		if errors.Is(err, os.ErrNotExist) {
			status = "not found -> defaults"
		} else if err != nil {
			status = "read failed"
		}
		*notes = append(*notes, fmt.Sprintf("Global config: %s (%s)", absolute, status))
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, invalidUserConfig(absolute, data, err)
	}
	c, err := Parse(data, filepath.Dir(absolute))
	if err != nil {
		return nil, invalidUserConfig(absolute, data, err)
	}
	n, err := document(data)
	if err != nil {
		return nil, invalidUserConfig(absolute, data, err)
	}
	if field(n, "trust") != nil {
		return nil, invalidUserConfig(absolute, data, errors.New("trust is only allowed in project configs"))
	}
	for i := range c.Hooks {
		c.Hooks[i].Source = absolute
	}
	return c, nil
}

// Parse accepts version omission only for empty files/mappings. Defaults are
// seeded before decoding so explicitly supplied zero limits remain invalid.
func Parse(data []byte, dir string) (*Config, error) {
	return parse(data, dir, true)
}

func parse(data []byte, dir string, strict bool) (*Config, error) {
	c := &Config{Dir: dir}
	c.ApplyDefaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(strict)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("config: multiple YAML documents are not supported")
	}
	n, err := document(data)
	if err != nil {
		return nil, err
	}
	if n != nil && n.Kind != yaml.MappingNode && n.Tag != "!!null" {
		return nil, fmt.Errorf("line %d: config must be a mapping", n.Line)
	}
	var problems []error
	if n != nil && len(n.Content) > 0 && field(n, "version") == nil {
		problems = append(problems, errors.New("version: required for non-empty config"))
	}
	hooks := field(n, "hooks")
	if hooks != nil && hooks.Kind == yaml.AliasNode {
		hooks = hooks.Alias
	}
	for i := range c.Hooks {
		var hn *yaml.Node
		if hooks != nil && i < len(hooks.Content) {
			hn = hooks.Content[i]
		}
		defaultHook(&c.Hooks[i], hn)
	}
	if err := c.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return c, nil
}

func document(data []byte) (*yaml.Node, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	if len(n.Content) == 0 {
		return nil, nil
	}
	return n.Content[0], nil
}
func field(n *yaml.Node, key string) *yaml.Node {
	return fieldThroughMerge(n, key, make(map[*yaml.Node]bool))
}

func (c *Config) HookCommandPath(h Hook) string {
	if len(h.Command) == 0 {
		return ""
	}
	p := h.Command[0]
	if !filepath.IsAbs(p) && strings.Contains(p, "/") {
		return filepath.Join(c.Dir, p)
	}
	return p
}

// LoadGhAliases reads only aliases from config.yml; unrelated gh keys are legal.
func LoadGhAliases(env map[string]string) (map[string]string, error) {
	d := env["GH_CONFIG_DIR"]
	if d == "" {
		if x := env["XDG_CONFIG_HOME"]; x != "" {
			d = filepath.Join(x, "gh")
		} else {
			d = filepath.Join(env["HOME"], ".config", "gh")
		}
	}
	p := filepath.Join(d, "config.yml")
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c struct {
		Aliases map[string]string `yaml:"aliases"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("gh aliases %s: %w", p, err)
	}
	if c.Aliases == nil {
		c.Aliases = map[string]string{}
	}
	return c.Aliases, nil
}
