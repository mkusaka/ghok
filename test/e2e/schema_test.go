//go:build unix

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

func TestHookInputsMatchPublishedSchema(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin []byte
	}{
		{name: "empty-argv"},
		{name: "native-texts", args: []string{"pr", "create", "-R", "acme/project", "-t", "Title", "-b", "Body"}},
		{name: "stdin-texts", args: []string{"pr", "comment", "7", "-R", "acme/project", "--body-file", "-"}, stdin: []byte("comment body")},
		{name: "REST-parameters", args: []string{"api", "repos/acme/project/issues", "-f", "title=Title", "-f", "body=Body", "-F", "labels[]=bug", "-F", "labels[]=docs"}},
		{name: "GraphQL", args: []string{"api", "graphql", "-f", `query=mutation { addComment(input:{subjectId:"opaque",body:"Body"}) { clientMutationId } }`}},
		{name: "unknown", args: []string{"future-command"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				first, second, post := s.hook("first", "pre"), s.hook("second", "pre"), s.hook("post", "post")
				second.DependsOn = []string{"first"}
				post.CaptureOutput = true
				c.Hooks = []config.Hook{first, second, post}
			})
			result := s.management(nil, "schema", "hook-input")
			result.code(t, 0)
			var schema map[string]any
			decodeJSON(t, result.stdout, &schema)
			s.gh(tc.stdin, tc.args...).code(t, 0)
			for _, id := range []string{"first", "second", "post"} {
				data, err := os.ReadFile(filepath.Join(s.root, id+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var input any
				decodeJSON(t, data, &input)
				checkHookSchemaKeys(t, schema, input, "$")
			}
		})
	}
}

// Check every emitted key recursively, including dependency and post-result
// records. Parameters deliberately permit arbitrary JSON, including nulls.
func checkHookSchemaKeys(t *testing.T, schema map[string]any, value any, path string) {
	t.Helper()
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s: want object, got %T", path, value)
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range object {
			property, ok := properties[key].(map[string]any)
			if !ok {
				property, ok = schema["additionalProperties"].(map[string]any)
				if !ok {
					t.Fatalf("%s: emitted key %q is absent from the schema", path, key)
				}
			}
			checkHookSchemaKeys(t, property, child, path+"."+key)
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			t.Fatalf("%s: want non-null array, got %T", path, value)
		}
		for i, child := range items {
			checkHookSchemaKeys(t, schema["items"].(map[string]any), child, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}
