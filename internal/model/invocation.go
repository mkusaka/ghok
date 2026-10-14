package model

// Invocation is everything a normalizer may look at. Normalizers are pure:
// they must not touch the filesystem, network, environment or stdin except
// through the fields below.
type Invocation struct {
	// Argv are the arguments after the executable name (os.Args[1:]).
	Argv []string
	// Env is the process environment (KEY -> value).
	Env map[string]string
	// Cwd is the invocation directory.
	Cwd string
	// Stdin is what the wrapper captured from stdin, if anything.
	Stdin StdinData
	// ReadFile reads a file for analysis (e.g. --input file, -F query=@file).
	// It is bounded by the caller (size limit). nil means files are unreadable;
	// normalizers must then report UnknownInputUnavailable, never guess.
	ReadFile func(path string) ([]byte, error)
	// Aliases are gh aliases (name -> expansion) as stored in gh's config.
	// Expansions starting with "!" are shell aliases.
	Aliases map[string]string
}

// StdinStatus describes whether stdin bytes are available to normalizers.
type StdinStatus string

const (
	StdinNotCaptured StdinStatus = "not-captured" // not needed or not attempted
	StdinCaptured    StdinStatus = "captured"     // Data holds all stdin bytes (byte-exact)
	StdinTTY         StdinStatus = "tty"          // stdin is a terminal; deliberately not read
	StdinTooLarge    StdinStatus = "too-large"    // exceeded analysis limit; Data is nil
	StdinTimeout     StdinStatus = "timeout"      // producer did not finish in time; Data is nil
	StdinError       StdinStatus = "error"        // read error; see Error
)

type StdinData struct {
	Status StdinStatus
	Data   []byte
	Error  string
}

// APIRequest is a parsed `gh api` invocation (see internal/normalize/apiargs).
type APIRequest struct {
	// Endpoint is the positional endpoint argument exactly as given:
	// "repos/{owner}/{repo}/pulls", "/repos/o/r", "graphql", or a full URL.
	Endpoint string
	// Method is the effective HTTP method (upper case), applying gh's rules:
	// explicit -X wins; otherwise POST if any field or --input was given; else GET.
	Method         string
	MethodExplicit bool
	Hostname       string // --hostname ("" = not given)
	Fields         []APIField
	Headers        []string // raw "key:value" as given to -H
	Input          string   // --input value; "" = none, "-" = stdin
	Paginate       bool
	Slurp          bool
	Silent         bool
	Include        bool
	Verbose        bool
	Previews       []string
	JQ             string
	Template       string
	Cache          string
}

// APIField is one -f/--raw-field (Typed=false) or -F/--field (Typed=true).
type APIField struct {
	Key     string
	Value   string
	Typed   bool
	NoValue bool // given without '=' (gh allows only "key[]" this way)
}

// ResolvedContext is what the resolver learned about the environment.
// Every field is optional; Sources says where each known value came from
// ("argv", "env", "git", "api") and Unresolved lists what could not be found.
type ResolvedContext struct {
	Host       string            `json:"host,omitempty"`
	Owner      string            `json:"owner,omitempty"`
	Repository string            `json:"repository,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	HeadSHA    string            `json:"headSha,omitempty"`
	GitDir     string            `json:"gitDir,omitempty"`
	Sources    map[string]string `json:"sources,omitempty"`
	Unresolved []Unresolved      `json:"unresolved,omitempty"`
}
