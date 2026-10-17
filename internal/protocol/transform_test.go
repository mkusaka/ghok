package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseTransformResponse(t *testing.T) {
	for _, data := range []string{"", " \n", `{"schemaVersion":1}`, `{"schemaVersion":1,"set":{"/operations/0/texts/title/value":"new","/operations/1/parameters/a~1b~0c":true},"unset":["/operations/2/target/number"]}`} {
		if _, err := ParseTransformResponse([]byte(data)); err != nil {
			t.Fatalf("valid %q: %v", data, err)
		}
	}
	for _, data := range []string{
		`null`, `[]`, `{}`, `{"schemaVersion":0}`, `{"schemaVersion":2}`, `{"schemaVersion":1.0}`, `{"schemaVersion":999999999999999999999}`,
		`{"schemaVersion":1,"decision":"allow"}`, `{"schemaVersion":1} {"schemaVersion":1}`, `{"schemaVersion":1}garbage`,
		`{"schemaVersion":1,"schemaVersion":1}`, `{"schemaVersion":1,"set":{"/operations/0/target/number":1,"/operations/0/target/number":2}}`,
		`{"schemaVersion":1,"set":null}`, `{"schemaVersion":1,"unset":null}`,
		`{"schemaVersion":1,"set":{"/operations/0/texts/body/value":"x"},"unset":["/operations/0/texts/body/value"]}`,
		`{"schemaVersion":1,"unset":["/operations/0/target/number","/operations/0/target/number"]}`,
		`{"schemaVersion":1,"set":{"/operations/01/texts/title/value":"x"}}`,
		`{"schemaVersion":1,"unset":["/operations/-/target/number"]}`,
		`{"schemaVersion":1,"unset":["/operations/0/target/host"]}`,
		`{"schemaVersion":1,"unset":["/operations/0/parameters/a/b"]}`,
		`{"schemaVersion":1,"unset":["/operations/0/parameters/a~2b"]}`,
		`{"schemaVersion":1,"unset":["/operations/0/action"]}`,
	} {
		if _, err := ParseTransformResponse([]byte(data)); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestParseTransformEnvironment(t *testing.T) {
	token := "ghs" + "_transform-test-token"
	for _, env := range []map[string]any{
		{},
		{"GH_TOKEN": token, "GITHUB_TOKEN": nil, "GH_PROMPT_DISABLED": "1", "_CUSTOM_1": "", "lowercase": "日本語\nvalue"},
	} {
		data, err := json.Marshal(map[string]any{"schemaVersion": 1, "env": env})
		if err != nil {
			t.Fatal(err)
		}
		response, err := ParseTransformResponse(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Env) != len(env) {
			t.Fatal("environment entries were lost")
		}
		for name, want := range env {
			got, exists := response.Env[name]
			if !exists || (want == nil && got != nil) || (want != nil && (got == nil || *got != want)) {
				t.Fatalf("environment entry %s did not round-trip", name)
			}
		}
	}
	for _, env := range []any{
		nil, []any{}, "text", 42, true,
		map[string]any{"CUSTOM": 42}, map[string]any{"CUSTOM": true},
		map[string]any{"CUSTOM": []any{token}}, map[string]any{"CUSTOM": map[string]any{"secret": token}},
		map[string]any{"CUSTOM": token + "\x00suffix"},
	} {
		data, err := json.Marshal(map[string]any{"schemaVersion": 1, "env": env})
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseTransformResponse(data)
		if err == nil {
			t.Fatal("accepted invalid environment")
		}
		if strings.Contains(err.Error(), token) {
			t.Fatal("environment value leaked in diagnostic")
		}
	}
	for _, name := range []string{
		"", "1CUSTOM", "A=B", "A-B", "A.B", "A B", "A\n", "A\x00B", "日本語",
		"GHOK_", "GHOK_EVENT_ID", "GH_PATH", "GH_CONFIG_DIR", "XDG_CONFIG_HOME", "HOME", "PATH",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_", "DYLD_INSERT_LIBRARIES", "GH_HOST", "GH_REPO",
	} {
		for _, value := range []any{token, nil} {
			data, err := json.Marshal(map[string]any{"schemaVersion": 1, "env": map[string]any{name: value}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseTransformResponse(data); err == nil {
				t.Fatalf("accepted forbidden or malformed environment name %q", name)
			} else if strings.Contains(err.Error(), token) {
				t.Fatal("environment value leaked in diagnostic")
			}
		}
	}
	for _, data := range []string{
		`{"schemaVersion":1,"env":{},"env":{}}`,
		`{"schemaVersion":1,"env":{"CUSTOM":"first","CUSTOM":"second"}}`,
		`{"schemaVersion":1,"env":{"CUSTOM":{"` + token + `":1,"` + token + `":2}}}`,
	} {
		_, err := ParseTransformResponse([]byte(data))
		if err == nil || strings.Contains(err.Error(), token) {
			t.Fatal("duplicate keys accepted or environment value exposed")
		}
	}
}
