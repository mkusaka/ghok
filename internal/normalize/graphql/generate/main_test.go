package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtractMutationMetadata(t *testing.T) {
	data := []byte(`
		schema { query: Query }
		extend schema { mutation: Changes }
		type Query { value: String }
		type Repository { id: ID! }
		interface PullRequest { id: ID! }
		union Issue = Repository
		input NestedInput { value: String! }
		input ReviewInput {
			repositoryId: ID!
			pullRequestId: ID
			subjectId: ID!
			id: ID
			issueId: ID
			ignoredId: String!
			ids: [ID!]!
			listId: [ID]
			nested: NestedInput!
			matrix: [[NestedInput!]!]!
		}
		extend input ReviewInput { extra: [String!] }
		input SingleInput { subjectId: ID! }
		type Changes {
			"""
			First description line.
			Second description line.
			"""
			updatePullRequest(input: ReviewInput!): String @deprecated
			addComment(input: SingleInput!): String @deprecated(reason: "Use another mutation")
		}
		extend type Changes { ping: String }
	`)
	got, err := extractMutations(data)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]mutationSchema{
		"updatePullRequest": {
			InputType: "ReviewInput",
			InputFields: []schemaInputField{
				{Name: "extra", Type: "[String!]"},
				{Name: "id", Type: "ID"},
				{Name: "ids", Type: "[ID!]!"},
				{Name: "ignoredId", Type: "String!"},
				{Name: "issueId", Type: "ID"},
				{Name: "listId", Type: "[ID]"},
				{Name: "matrix", Type: "[[NestedInput!]!]!"},
				{Name: "nested", Type: "NestedInput!"},
				{Name: "pullRequestId", Type: "ID"},
				{Name: "repositoryId", Type: "ID!"},
				{Name: "subjectId", Type: "ID!"},
			},
			IDFields: []schemaIDField{
				{Name: "id"},
				{Name: "issueId", Kind: "Issue"},
				{Name: "pullRequestId", Kind: "PullRequest"},
				{Name: "repositoryId", Kind: "Repository"},
				{Name: "subjectId"},
			},
			PrimaryID:         "pullRequestId",
			Description:       "First description line.",
			Deprecated:        true,
			DeprecationReason: "No longer supported",
		},
		"addComment": {
			InputType:         "SingleInput",
			InputFields:       []schemaInputField{{Name: "subjectId", Type: "ID!"}},
			IDFields:          []schemaIDField{{Name: "subjectId"}},
			PrimaryID:         "subjectId",
			Deprecated:        true,
			DeprecationReason: "Use another mutation",
		},
		"ping": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}

func TestPrimaryID(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutation   string
		candidates []schemaIDField
		want       string
	}{
		{"longest noun", "submitPullRequestReview", []schemaIDField{{Name: "pullRequestId", Kind: "PullRequest"}, {Name: "pullRequestReviewId", Kind: "PullRequestReview"}}, "pullRequestReviewId"},
		{"case insensitive", "UPDATEPULLREQUEST", []schemaIDField{{Name: "repositoryId", Kind: "Repository"}, {Name: "pullRequestId", Kind: "PullRequest"}}, "pullRequestId"},
		{"unknown kind noun", "updateSubject", []schemaIDField{{Name: "subjectId"}, {Name: "repositoryId", Kind: "Repository"}}, "subjectId"},
		{"ambiguous", "linkObjects", []schemaIDField{{Name: "repositoryId", Kind: "Repository"}, {Name: "issueId", Kind: "Issue"}}, ""},
		{"tied nouns", "updateSubject", []schemaIDField{{Name: "firstId", Kind: "Subject"}, {Name: "otherId", Kind: "Subject"}}, ""},
		{"single", "addComment", []schemaIDField{{Name: "subjectId"}}, "subjectId"},
		{"bare id", "deleteIssueComment", []schemaIDField{{Name: "id"}}, "id"},
		{"bare id with noun", "deleteIssue", []schemaIDField{{Name: "id"}, {Name: "issueId", Kind: "Issue"}}, "issueId"},
		{"bare id ambiguous", "deleteComment", []schemaIDField{{Name: "id"}, {Name: "issueId", Kind: "Issue"}}, ""},
		{"none", "ping", nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := primaryID(test.mutation, test.candidates); got != test.want {
				t.Fatalf("primaryID = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSchemaSHA256MismatchPreservesOutput(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.graphql")
	output := filepath.Join(dir, "snapshot.go")
	// Deliberately invalid GraphQL proves the pin is checked before parsing.
	if err := os.WriteFile(schema, []byte("not a schema"), 0600); err != nil {
		t.Fatal(err)
	}
	original := []byte("existing snapshot\n")
	if err := os.WriteFile(output, original, 0600); err != nil {
		t.Fatal(err)
	}
	err := generate(schema, "2026-10-15", output, fmt.Sprintf("%x", sha256.Sum256([]byte("different schema"))))
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("error = %v, want SHA256 mismatch", err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("output = %q, want preserved %q", got, original)
	}
}

func TestMalformedSchemaReturnsError(t *testing.T) {
	for _, schema := range []string{"", "type Mutation { broken(input: [ID!): String }", "type Query { value: String }"} {
		if _, err := extractMutations([]byte(schema)); err == nil {
			t.Fatalf("extractMutations(%q) returned no error", schema)
		}
	}
}
