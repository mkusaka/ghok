package schema

import (
	"encoding/json"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func hookDocument(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestHookInputSchema(t *testing.T) {
	s := mustSchema(t, "hook-input")
	input := protocol.Input{
		SchemaVersion: model.SchemaVersion, Event: protocol.EventPre,
		Invocation:     protocol.InvocationInfo{Via: "shim"},
		Classification: model.Classification{Level: model.LevelL1, Confidence: model.ConfidenceExact, Resolution: model.ResolutionComplete},
	}
	for _, operations := range [][]model.Operation{nil, {}} {
		input.Operations = operations
		doc := hookDocument(t, input)
		if _, present := doc["operations"]; present {
			t.Fatal("empty operations must be omitted, not null or an empty array")
		}
		invocation := doc["invocation"].(map[string]any)
		if _, present := invocation["argv"]; present {
			t.Fatal("empty argv must be omitted")
		}
		if err := checkSchema(s, doc, "$"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"missing-version", func(doc map[string]any) { delete(doc, "schemaVersion") }},
		{"future-version", func(doc map[string]any) { doc["schemaVersion"] = 2 }},
		{"invalid-event", func(doc map[string]any) { doc["event"] = "during" }},
		{"null-operations", func(doc map[string]any) { doc["operations"] = nil }},
		{"null-argv", func(doc map[string]any) { doc["invocation"].(map[string]any)["argv"] = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := hookDocument(t, input)
			tc.edit(doc)
			if err := checkSchema(s, doc, "$"); err == nil {
				t.Fatal("invalid hook input accepted")
			}
		})
	}
	input.Operations = []model.Operation{{
		Family: "pr.merge", Action: "pr.merge.direct", Effect: model.EffectWrite, Execution: model.ExecutionWillRun,
		Target:     model.Target{Kind: "pullRequest", Number: model.IntPtr(7)},
		Parameters: map[string]any{"custom": map[string]any{"values": []any{nil, "text", 1, true}}},
	}}
	doc := hookDocument(t, input)
	if err := checkSchema(s, doc, "$"); err != nil {
		t.Fatalf("known integer pointer or arbitrary parameter values rejected: %v", err)
	}
	op := doc["operations"].([]any)[0].(map[string]any)
	op["target"].(map[string]any)["number"] = "7"
	if err := checkSchema(s, doc, "$"); err == nil {
		t.Fatal("string target number accepted")
	}
}

func TestHookResponseSchema(t *testing.T) {
	s := mustSchema(t, "hook-response")
	for _, version := range []int{0, 1} {
		for _, decision := range []protocol.Decision{protocol.DecisionAllow, protocol.DecisionDeny, protocol.DecisionAbstain} {
			doc := hookDocument(t, protocol.Response{SchemaVersion: version, Decision: decision})
			if err := checkSchema(s, doc, "$"); err != nil {
				t.Fatalf("valid response rejected: %v", err)
			}
		}
	}
	for _, doc := range []map[string]any{
		{}, {"decision": "audit"}, {"decision": "deny", "schemaVersion": 0},
		{"decision": "deny", "schemaVersion": 2}, {"decision": "deny", "schemaVersion": "1"},
	} {
		if err := checkSchema(s, doc, "$"); err == nil {
			t.Fatalf("invalid response accepted: %v", doc)
		}
	}
}
