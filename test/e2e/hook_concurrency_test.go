//go:build unix

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHookConcurrencyItems(t *testing.T) {
	s := newSandbox(t)
	hook := func(id string) string {
		return fmt.Sprintf("{id: %s, command: [%q], env: {FAKEGH_MODE: hook, FAKEGH_HOOK_RECORD: %q}}", id, fakeBinary, filepath.Join(s.root, id+".json"))
	}
	config := "version: 1\nlogging: {enabled: false}\nhooks:\n  pre:\n" +
		"    - " + hook("first") + "\n" +
		"    - parallel:\n        - " + hook("b") + "\n        - " + hook("c") + "\n" +
		"    - wait-all:\n" +
		"    - " + hook("last") + "\n"
	if err := os.WriteFile(s.cfg, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	s.gh(nil, "version").code(t, 0)
	for _, id := range []string{"first", "b", "c", "last"} {
		if in := s.hookInput(id); in.HookID != id {
			t.Fatalf("%s input: %+v", id, in)
		}
	}
	result := s.management(nil, "hooks", "list", "--json")
	result.code(t, 0)
	var listed []struct {
		ID         string   `json:"id"`
		Background bool     `json:"background"`
		Wait       []string `json:"wait"`
	}
	decodeJSON(t, result.stdout, &listed)
	if len(listed) != 4 || !listed[1].Background || !listed[2].Background || listed[3].Background || !reflect.DeepEqual(listed[3].Wait, []string{"b", "c"}) {
		t.Fatalf("hooks list: %+v", listed)
	}

	bad := "version: 1\nhooks:\n  pre:\n    - " + hook("first") + "\n    - wait: first\n"
	if err := os.WriteFile(s.cfg, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	result = s.management(nil, "config", "validate")
	result.code(t, 1)
	if !bytes.Contains(result.stderr, []byte("first is not an earlier background hook in this phase list")) {
		t.Fatalf("validate: %s", result.stderr)
	}
}
