package schema

import "testing"

func TestTransformConfigSchema(t *testing.T) {
	for _, name := range []string{"config", "project-config"} {
		s := mustSchema(t, name)
		transform := map[string]any{"id": "edit", "command": []any{"./filter"}, "onError": "deny"}
		doc := map[string]any{"version": 1, "transforms": map[string]any{"pre": []any{transform}}}
		if err := checkSchema(s, doc, "$"); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"parallel", "background", "wait", "wait-all", "dependsOn", "optional", "captureOutput", "enabled", "phase"} {
			transform[key] = nil
			if err := checkSchema(s, doc, "$"); err == nil {
				t.Fatalf("%s accepted %s", name, key)
			}
			delete(transform, key)
		}
		transform["files"] = []any{"dependency"}
		err := checkSchema(s, doc, "$")
		if (err == nil) != (name == "project-config") {
			t.Fatalf("%s files: %v", name, err)
		}
		delete(transform, "files")
		doc["transforms"] = map[string]any{"post": nil}
		if err := checkSchema(s, doc, "$"); err == nil {
			t.Fatalf("%s accepted reserved post", name)
		}
	}
}

func TestTransformResponseSchema(t *testing.T) {
	s := mustSchema(t, "transform-response")
	if err := checkSchema(s, map[string]any{"schemaVersion": 1, "set": map[string]any{"/operations/0/texts/title/value": "new"}}, "$"); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []map[string]any{{}, {"schemaVersion": 0}, {"schemaVersion": 1, "decision": "allow"}} {
		if err := checkSchema(s, doc, "$"); err == nil {
			t.Fatalf("accepted %v", doc)
		}
	}
	props := s["properties"].(map[string]any)
	if props["set"].(map[string]any)["propertyNames"] == nil || props["unset"].(map[string]any)["uniqueItems"] != true {
		t.Fatal("response paths lack constraints")
	}
}
