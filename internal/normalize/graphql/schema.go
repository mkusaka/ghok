package graphql

// Schema metadata is generated at development time; type signatures preserve
// named types, lists and non-null markers without loading a schema at runtime.
type mutationSchema struct {
	InputType         string
	InputFields       []schemaInputField
	IDFields          []schemaIDField
	PrimaryID         string
	Description       string
	Deprecated        bool
	DeprecationReason string
}

type schemaInputField struct {
	Name string
	Type string
}

type schemaIDField struct {
	Name string
	Kind string // Empty when the field name does not identify a schema object kind.
}
