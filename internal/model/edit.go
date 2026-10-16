package model

// EditLocator is private normalization bookkeeping. Keys in Operation.Editable
// are relative semantic JSON pointers, never part of the public JSON contract.
type EditLocator struct {
	Kind      string
	Flag      string
	ValueType string
	Spans     []ArgSpan
	// Key identifies a request field or a path within a JSON input object.
	Key   []string
	Input string
	// Parameter and Text identify alternate semantic views of the same value.
	Parameter string
	Text      string
}

// ArgSpan identifies argv tokens consumed by a flag or positional. Keep retains
// unrelated shorthand flags from the same token when replacing/removing it.
type ArgSpan struct {
	Start int
	End   int
	Keep  []string
	// KeepGroups preserves each retained flag's argument boundary when several
	// edits remove different flags from the same shorthand bundle.
	KeepGroups [][]string
}
