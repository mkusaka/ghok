package protocol

import "testing"

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
